package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"idiom/internal/config"
	"idiom/internal/frontier/workerruntime"
	"idiom/internal/llama"
	"idiom/internal/workspace"
)

func TestPlanThenExecuteWriteGuardRejectsEditFile(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
		},
	}
	got := runner.preflightPlanThenExecuteWriteGuard(workspace.ToolRequest{
		Tool: "edit_file",
		Path: "a.txt",
	})
	if !got.Intercepted {
		t.Fatalf("planner phase must reject edit_file; got %#v", got)
	}
	if got.Result.Tool != "edit_file" {
		t.Fatalf("rejection should keep the tool name (so the audit trail shows what was blocked), got %q", got.Result.Tool)
	}
	if !strings.Contains(got.Result.Error, "submit_plan") {
		t.Fatalf("rejection must point at submit_plan, got %q", got.Result.Error)
	}
	if !strings.Contains(got.Result.Error, "executor phase") {
		t.Fatalf("rejection should explain the executor will dispatch later, got %q", got.Result.Error)
	}
}

func TestPlanThenExecuteLoopBudgetRecoveryRequiresTerminalOutcome(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/productworkflow/coverage_gate.go lines 1-10 (total lines: 20)",
		Output:  "1:package productworkflow\n2:\n3:func EvaluateCoverageGate() {}\n",
	}}
	recovery, ok := runner.planThenExecuteLoopBudgetRecoveryPrompt(workspace.ToolRequest{
		Tool:       "code_navigate",
		Path:       "internal/productworkflow/coverage_gate.go",
		Capability: "definition",
		Pattern:    "EvaluateCoverageGate",
	}, results, "code_navigate repeated after warning", false)
	if !ok {
		t.Fatal("expected loop-budget recovery once source evidence makes terminal planning available")
	}
	if !recovery.TerminalOnly {
		t.Fatalf("single-candidate loop-budget recovery should force terminal outcome:\n%s", recovery.Prompt)
	}
	for _, want := range []string{"loop budget", "submit_plan", "Decision: source-satisfied", "<<QUESTION>>"} {
		if !strings.Contains(recovery.Prompt, want) {
			t.Fatalf("recovery prompt missing %q:\n%s", want, recovery.Prompt)
		}
	}
	if strings.Contains(recovery.Prompt, "perform one bounded read") {
		t.Fatalf("loop-budget terminal recovery must not ask for another read:\n%s", recovery.Prompt)
	}
}

func TestPlanThenExecuteWriteGuardRejectsAllPerEditWrites(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
		},
	}
	for _, tool := range []string{
		"edit_file", "write_file", "append_file", "apply_patch",
		"delegate_edit", "go_move_declarations", "mkdir",
	} {
		got := runner.preflightPlanThenExecuteWriteGuard(workspace.ToolRequest{Tool: tool})
		if !got.Intercepted {
			t.Fatalf("planner phase must reject %q, got %#v", tool, got)
		}
	}
}

func TestPlanThenExecuteWriteGuardLetsSubmitPlanThrough(t *testing.T) {
	// submit_plan is the planner's ONE write — without this exemption
	// the guard would intercept it and the lane could never produce a
	// plan.
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
		},
	}
	got := runner.preflightPlanThenExecuteWriteGuard(workspace.ToolRequest{
		Tool: "submit_plan",
	})
	if got.Intercepted {
		t.Fatalf("submit_plan must pass through during planner phase, got %#v", got)
	}
}

func TestMLXPlanThenExecuteAdvertisesTextualSubmitPlanProtocol(t *testing.T) {
	t.Skip("MLX now supports native tools; this validates the old textual protocol")
	runner := &Runner{
		Client: llama.NewClient("http://example.invalid", time.Second).WithBackend(config.BackendMLX),
	}
	brief := workerruntime.NormalizeRuntimeBrief(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"submit_plan", "read_file"},
		RequiredTools:     []string{"submit_plan"},
		RequiredEditPaths: []string{"a.txt"},
	})
	got := runner.textualToolProtocolPrompt(brief)
	for _, want := range []string{
		"# Textual Tool Call Protocol",
		"exactly one JSON object",
		"top-level `tool`, `rationale`, and `edits`",
		"`edits` must be an array",
		"never copy placeholders",
		"schema examples",
		"ellipses",
		"`existing code` comments",
		"no prose",
		"XML tags",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in textual protocol prompt:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "submit_plan") || !strings.Contains(got, "read_file") {
		t.Fatalf("prompt should include required tool first and bounded allowed examples:\n%s", got)
	}
	for _, forbidden := range []string{"relative/file", "exact current substring", "replacement text", "one paragraph on the overall plan"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("textual protocol prompt must not include copyable placeholder %q:\n%s", forbidden, got)
		}
	}
}

func TestNativeToolBackendSkipsTextualToolProtocolPrompt(t *testing.T) {
	runner := &Runner{
		Client: llama.NewClient("http://example.invalid", time.Second).WithBackend(config.BackendLlama),
	}
	brief := workerruntime.NormalizeRuntimeBrief(workerruntime.WorkerRuntimeBrief{
		AllowedTools:  []string{"submit_plan"},
		RequiredTools: []string{"submit_plan"},
	})
	if got := runner.textualToolProtocolPrompt(brief); got != "" {
		t.Fatalf("native-tool backend should not receive duplicate textual protocol prompt:\n%s", got)
	}
}

func TestPlanExecuteCompletionGateRecoveryKeepsSubmitPlanBudget(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.txt"},
	}
	got := completionGateRecoveryChatOptions(llama.ChatOptions{MaxTokens: 16384}, brief, nil)
	if got.MaxTokens != 768 {
		t.Fatalf("plan-execute source-read recovery should cap to one tool call, got %d", got.MaxTokens)
	}
	results := []workspace.ToolResult{{Tool: "read_file", Summary: "Read f1 a.txt lines 1-10 (total lines: 10)"}}
	got = completionGateRecoveryChatOptions(llama.ChatOptions{MaxTokens: 16384}, brief, results)
	if got.MaxTokens != 4096 {
		t.Fatalf("plan-execute existing-file recovery should cap submit_plan budget, got %d", got.MaxTokens)
	}
	got = completionGateRecoveryChatOptions(llama.ChatOptions{}, brief, results)
	if got.MaxTokens != 4096 {
		t.Fatalf("plan-execute existing-file recovery should default to capped submit_plan budget, got %d", got.MaxTokens)
	}
}

func TestPlanThenExecuteSubmitPlanCoverageGuardRejectsMissingRequiredPath(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "a", NewText: "A"},
		},
	})
	if !got.Intercepted {
		t.Fatalf("submit_plan missing a required path must be rejected")
	}
	if !strings.Contains(got.Result.Error, "b.txt") {
		t.Fatalf("rejection should name the missing path, got %q", got.Result.Error)
	}
	details, ok := planThenExecuteSubmitPlanCoverageDetailsFromResult(got.Result)
	if !ok {
		t.Fatalf("rejection should include typed coverage details, got output %q", got.Result.Output)
	}
	if len(details.MissingRequiredPaths) != 1 || details.MissingRequiredPaths[0] != "b.txt" {
		t.Fatalf("typed coverage details should name missing path, got %#v", details.MissingRequiredPaths)
	}
	if !strings.Contains(got.Result.Error, "corrected plan") {
		t.Fatalf("rejection should be recoverable, got %q", got.Result.Error)
	}
}

func TestPlanThenExecuteSubmitPlanCoverageGuardEnforcesAllowedScopeWithoutCoverage(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:       PlanThenExecutePlannerTaskFamily,
			AllowedEditPaths: []string{"a.txt", "b.txt"},
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "a", NewText: "A"},
		},
	})
	if got.Intercepted {
		t.Fatalf("allowed_edit_paths are scope only, not coverage obligations: %#v", got)
	}

	got = runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{Path: "c.txt", OldText: "c", NewText: "C"},
		},
	})
	if !got.Intercepted {
		t.Fatalf("submit_plan outside allowed_edit_paths must be rejected")
	}
	if !strings.Contains(got.Result.Error, "outside allowed_edit_paths") || !strings.Contains(got.Result.Error, "c.txt") {
		t.Fatalf("allowed-scope rejection should name the outside path, got %q", got.Result.Error)
	}
}

func TestPlanThenExecuteUngroundedSubmitPlanRejectsUnscopedExistingFileEditWithoutAnchor(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "target.txt", "old\n"); err != nil {
		t.Fatalf("write target: %v", err)
	}
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanGroundingGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{{
			Path:       "target.txt",
			ReplaceAll: true,
			NewText:    "new\n",
			Purpose:    "replace target",
		}},
	}, nil)
	if !got.Intercepted {
		t.Fatal("unscoped submit_plan must require source-body evidence for existing-file edits")
	}
	if !strings.Contains(got.Result.Error, "target.txt") || !strings.Contains(got.Result.Error, "source-body evidence") {
		t.Fatalf("grounding rejection should name the path and evidence requirement, got %q", got.Result.Error)
	}
}

func TestPlanThenExecuteUngroundedSubmitPlanAllowsSourceReadEdit(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "target.txt", "old\n"); err != nil {
		t.Fatalf("write target: %v", err)
	}
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanGroundingGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{{
			Path:    "target.txt",
			OldText: "old\n",
			NewText: "new\n",
			Purpose: "update target",
		}},
	}, []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read target.txt lines 1-1 (total lines: 1)",
		Output:  "old\n",
	}})
	if got.Intercepted {
		t.Fatalf("source-body evidence should ground the unscoped submit_plan edit, got %#v", got)
	}
}

func TestPlanThenExecuteScopedSubmitPlanRequiresGroundingBeyondPathScope(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "target.txt", "old\n"); err != nil {
		t.Fatalf("write target: %v", err)
	}
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"target.txt"},
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanGroundingGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{{
			Path:       "target.txt",
			ReplaceAll: true,
			NewText:    "new\n",
			Purpose:    "replace target",
		}},
	}, nil)
	if !got.Intercepted {
		t.Fatal("required path scope alone must not ground an existing-file submit_plan edit")
	}
	if !strings.Contains(got.Result.Error, "target.txt") {
		t.Fatalf("grounding rejection should name target.txt, got %q", got.Result.Error)
	}
}

func TestPlanThenExecuteScopedSubmitPlanExactAnchorGroundsEdit(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "target.txt", "old\n"); err != nil {
		t.Fatalf("write target: %v", err)
	}
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"target.txt"},
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanGroundingGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{{
			Path:    "target.txt",
			OldText: "old\n",
			NewText: "new\n",
			Purpose: "update target",
		}},
	}, nil)
	if got.Intercepted {
		t.Fatalf("exact current-source old_text anchor should ground scoped submit_plan edit, got %#v", got)
	}
}

func TestPlanThenExecuteSubmitPlanCompleteNewFileBodyGroundsEdit(t *testing.T) {
	root := t.TempDir()
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"new_target.txt"},
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanGroundingGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{{
			Path:       "new_target.txt",
			ReplaceAll: true,
			NewText:    "created\n",
			Purpose:    "create target",
		}},
	}, nil)
	if got.Intercepted {
		t.Fatalf("complete new-file body should ground submit_plan edit, got %#v", got)
	}
}

func TestPlanThenExecuteUnscopedSubmitPlanCoverageGuardRejectsInvalidAnchor(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "target.txt", "old\n"); err != nil {
		t.Fatalf("write target: %v", err)
	}
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{{
			Path:    "target.txt",
			OldText: "missing\n",
			NewText: "new\n",
			Purpose: "update target",
		}},
	})
	if !got.Intercepted {
		t.Fatal("unscoped submit_plan must still validate executable current-source anchors")
	}
	if !strings.Contains(got.Result.Error, "old_text not found") {
		t.Fatalf("expected invalid-anchor rejection, got %q", got.Result.Error)
	}
}

func TestDeterministicPlanExecuteGroundingRecoveryReadsRejectedMissingSourcePath(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   PlanThenExecutePlannerTaskFamily,
			Objective:    "expose read-only product workflow status and effort browsing through idiom tool direct-tool surface",
			AllowedTools: []string{"context_pack", "search_content", "product_efforts", "product_status"},
		},
	}
	req, _, ok := runner.deterministicPlanExecuteGroundingRecoveryRequest("I need more evidence.", []workspace.ToolResult{
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: edit paths lack source grounding",
			Error:   "missing source-body evidence for `cmd/idiom/main.go`",
		},
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote view",
			Output:  `{"candidate_files":[{"path":"internal/agent/runner.go","role":"editable"}]}`,
		},
	})
	if !ok {
		t.Fatal("expected deterministic grounding recovery after ungrounded submit_plan and context_pack")
	}
	if req.Tool != "read_file" || req.Path != "cmd/idiom/main.go" || req.StartLine != 1 || req.EndLine != 180 {
		t.Fatalf("expected bounded source read for rejected missing-source path, got %#v", req)
	}
}

func TestDeterministicPlanExecuteGroundingRecoveryPrioritizesRejectedMissingSourcePathOverSearchHit(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
			Objective:  "expose read-only product workflow status and effort browsing through idiom tool direct-tool surface",
		},
	}
	req, _, ok := runner.deterministicPlanExecuteGroundingRecoveryRequest("Still not ready.", []workspace.ToolResult{
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: edit paths lack source grounding",
			Error:   "missing source-body evidence for `cmd/idiom/main.go`",
		},
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote view",
			Output:  `{"candidate_files":[{"path":"internal/agent/runner.go","role":"editable"}]}`,
		},
		{
			Tool:    "search_content",
			Summary: "Search results",
			Output:  "internal/workspace/product_tools.go:12:func (m *Manager) productEfforts(req ToolRequest) ToolResult {\ninternal/agent/runner.go:10:plan-execute product workflow",
		},
	})
	if !ok {
		t.Fatal("expected deterministic grounding recovery to read the rejected missing-source path")
	}
	if req.Tool != "read_file" || req.Path != "cmd/idiom/main.go" {
		t.Fatalf("expected rejected missing-source path read before broad fallback, got %#v", req)
	}
}

func TestDeterministicPlanExecuteChainedSearchRecoveryReadsContextPackCandidate(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
			Contract:   "implementation_result",
			Objective:  "implement coverage gate authority reuse",
		},
	}
	state := &runnerExecutionState{pendingSearchInspection: true}
	req, _, ok := runner.deterministicPlanExecuteChainedSearchRecovery(
		workspace.ToolRequest{Tool: "search_content", Pattern: "coverage_gate"},
		[]workspace.ToolResult{
			{
				Tool:    "context_pack",
				Summary: "Context pack wrote view",
				Output:  `{"candidate_files":[{"path":"internal/productworkflow/coverage_gate.go","role":"editable"},{"path":"cmd/idiom/product_workflow.go","role":"editable"}]}`,
			},
			{
				Tool:    "search_content",
				Summary: "Found 400 matching line(s) (showing first 50).",
				Output:  "AGENTS.md:119:plan-execute prompt scaffolding",
			},
		},
		state,
	)
	if !ok {
		t.Fatal("expected chained-search recovery to read the top context-pack candidate")
	}
	if req.Tool != "read_file" || req.Path != "internal/productworkflow/coverage_gate.go" {
		t.Fatalf("unexpected recovery request: %#v", req)
	}
}

func TestDeterministicPlanExecuteGroundingRecoveryDoesNotStealTerminalReply(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
			Objective:  "expose read-only product workflow status and effort browsing through idiom tool direct-tool surface",
		},
	}
	_, _, ok := runner.deterministicPlanExecuteGroundingRecoveryRequest("Decision: source-satisfied\nPath: internal/workspace/product_tools.go", []workspace.ToolResult{
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: edit paths lack source grounding",
			Error:   "missing source-body evidence for `cmd/idiom/main.go`",
		},
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote view",
			Output:  `{"candidate_files":[{"path":"internal/agent/runner.go","role":"editable"}]}`,
		},
	})
	if ok {
		t.Fatal("terminal source-satisfied replies should go to the completion gate, not deterministic read recovery")
	}
}

func TestPlanThenExecuteSubmitPlanCoverageGuardRejectsPlaceholderBodiesWithTypedPath(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.go"},
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{{
			Path:    "a.go",
			OldText: "func run() {\n}\n",
			NewText: "func run() {\n\t// ... existing code ...\n}\n",
			Purpose: "update run",
		}},
	})
	if !got.Intercepted {
		t.Fatalf("submit_plan placeholder body must be rejected")
	}
	if !strings.Contains(got.Result.Error, "placeholder edit body path") || !strings.Contains(got.Result.Error, "a.go") {
		t.Fatalf("rejection should name placeholder path, got %q", got.Result.Error)
	}
	details, ok := planThenExecuteSubmitPlanCoverageDetailsFromResult(got.Result)
	if !ok {
		t.Fatalf("rejection should include typed coverage details, got output %q", got.Result.Output)
	}
	if len(details.PlaceholderEditBodies) != 1 || !strings.Contains(details.PlaceholderEditBodies[0], "a.go") {
		t.Fatalf("typed coverage details should name placeholder path, got %#v", details.PlaceholderEditBodies)
	}
}

func TestPlanThenExecuteSubmitPlanCoverageGuardRejectsEditWithoutPathExplicitly(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.txt"},
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{OldText: "a", NewText: "A", Purpose: "patch a required file"},
		},
	})
	if !got.Intercepted {
		t.Fatalf("submit_plan edit without path must be rejected")
	}
	for _, want := range []string{
		"edit entry missing `path`",
		"edits[0] patch a required file",
		"`edits` as a JSON array, not a quoted string",
		"complete executable source text",
		"not placeholders, ellipses, or prose",
	} {
		if !strings.Contains(got.Result.Error, want) {
			t.Fatalf("rejection should contain %q, got %q", want, got.Result.Error)
		}
	}
}

func TestPlanThenExecuteSubmitPlanCoverageGuardRejectsOutsideRequiredPath(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.txt"},
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "a", NewText: "A"},
			{Path: "surprise.txt", OldText: "x", NewText: "X"},
		},
	})
	if !got.Intercepted {
		t.Fatalf("submit_plan outside required paths must be rejected")
	}
	if !strings.Contains(got.Result.Error, "surprise.txt") {
		t.Fatalf("rejection should name the outside path, got %q", got.Result.Error)
	}
}

func TestPlanThenExecuteSubmitPlanCoverageGuardAcceptsFullCoverage(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "a", NewText: "A"},
			{Path: "b.txt", OldText: "b", NewText: "B"},
		},
	})
	if got.Intercepted {
		t.Fatalf("submit_plan with full required path coverage must pass, got %#v", got)
	}
}

func TestPlanThenExecuteSubmitPlanCoverageGuardTreatsRequiredDirectoryAsScope(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go"} {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte("package main\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{
				"cmd/idiom",
				"cmd/idiom/product_workflow.go",
				"cmd/idiom/product_workflow_test.go",
			},
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{Path: "cmd/idiom/product_workflow.go", OldText: "package main", NewText: "package main\n// updated"},
			{Path: "cmd/idiom/product_workflow_test.go", OldText: "package main", NewText: "package main\n// covered"},
		},
	})
	if got.Intercepted {
		t.Fatalf("submit_plan should not require an edit entry for the directory scope, got %#v", got)
	}

	got = runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{Path: "cmd/idiom/product_workflow.go", OldText: "package main", NewText: "package main\n// updated"},
		},
	})
	if !got.Intercepted {
		t.Fatalf("submit_plan missing the concrete required test file must still be rejected")
	}
	details, ok := planThenExecuteSubmitPlanCoverageDetailsFromResult(got.Result)
	if !ok {
		t.Fatalf("rejection should include typed coverage details, got output %q", got.Result.Output)
	}
	if slices.Contains(details.MissingRequiredPaths, "cmd/idiom") {
		t.Fatalf("directory scope must not be reported as missing file coverage, got %#v", details.MissingRequiredPaths)
	}
	if !slices.Contains(details.MissingRequiredPaths, "cmd/idiom/product_workflow_test.go") {
		t.Fatalf("missing concrete file coverage should still be reported, got %#v", details.MissingRequiredPaths)
	}
}

func TestPlanThenExecuteSubmitPlanCoverageGuardAllowsDirectoryScopeChildrenOnly(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cmd", "idiom"), 0o755); err != nil {
		t.Fatalf("mkdir cmd/idiom: %v", err)
	}
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"cmd/idiom"},
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{Path: "cmd/idiom/new_feature.go", Content: "package main\n"},
		},
	})
	if got.Intercepted {
		t.Fatalf("submit_plan child edit under directory scope should pass, got %#v", got)
	}

	got = runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{Path: "docs/out-of-scope.md", Content: "outside\n"},
		},
	})
	if !got.Intercepted {
		t.Fatalf("submit_plan outside directory scope must be rejected")
	}
	if !strings.Contains(got.Result.Error, "docs/out-of-scope.md") {
		t.Fatalf("outside-scope rejection should name the path, got %q", got.Result.Error)
	}
}

func TestPlanExecuteRemainingRequiredEditPathsExcludeDirectoryScope(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go"} {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte("package main\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{
				"cmd/idiom",
				"cmd/idiom/product_workflow.go",
				"cmd/idiom/product_workflow_test.go",
			},
		},
	}
	missing := runner.remainingRequiredEditPaths(nil)
	if slices.Contains(missing, "cmd/idiom") {
		t.Fatalf("directory scope must not appear as a remaining required file, got %#v", missing)
	}
	for _, want := range []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go"} {
		if !slices.Contains(missing, want) {
			t.Fatalf("missing required file %q, got %#v", want, missing)
		}
	}
}

func TestPlanExecuteUnreadRequiredPathsExcludeParentDirectoryScope(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{
			"cmd/idiom",
			"cmd/idiom/product_workflow.go",
			"cmd/idiom/product_workflow_test.go",
		},
	}
	unread := planThenExecuteUnreadRequiredPaths(brief, nil)
	if _, ok := unread["cmd/idiom"]; ok {
		t.Fatalf("parent directory scope must not become a source-body obligation, got %#v", unread)
	}
	for _, want := range []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go"} {
		if _, ok := unread[want]; !ok {
			t.Fatalf("missing unread required file %q, got %#v", want, unread)
		}
	}
}

func TestPlanThenExecuteSubmitPlanCoverageGuardAcceptsPlanArtifactEdits(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.txt"},
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		PlanArtifact: &workspace.PlanArtifact{
			Edits: []workspace.PlanEdit{{Path: "a.txt", OldText: "a", NewText: "A"}},
		},
	})
	if got.Intercepted {
		t.Fatalf("submit_plan plan_artifact.edits with full required path coverage must pass, got %#v", got)
	}
}

func TestPlanThenExecuteSubmitPlanCoverageGuardValidatesPersistedPlanArtifactEditsOnly(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "a", NewText: "A"},
			{Path: "b.txt", OldText: "b", NewText: "B"},
		},
		PlanArtifact: &workspace.PlanArtifact{
			Edits: []workspace.PlanEdit{{Path: "a.txt", OldText: "a", NewText: "A"}},
		},
	})
	if !got.Intercepted {
		t.Fatalf("coverage guard must reject when persisted plan_artifact.edits misses a required path")
	}
	if !strings.Contains(got.Result.Error, "b.txt") {
		t.Fatalf("rejection should name the missing persisted path, got %q", got.Result.Error)
	}
}

func TestPlanThenExecuteSubmitPlanCoverageGuardRejectsMissingRequiredSnippet(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("old behavior\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.txt"},
			RequiredEditSnippets: []workerruntime.RequiredEditSnippet{{
				ID:      "required_edit_2",
				Path:    "a.txt",
				Content: "old behavior\nnew regression coverage",
			}},
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "old behavior", NewText: "old behavior\n#include \"only-one-edit\""},
		},
	})
	if !got.Intercepted {
		t.Fatalf("submit_plan missing a required final-state snippet must be rejected")
	}
	if !strings.Contains(got.Result.Error, "required_edit_2") || !strings.Contains(got.Result.Error, "required_edit_snippets") {
		t.Fatalf("rejection should name the missing required snippet, got %q", got.Result.Error)
	}
}

func TestPlanThenExecuteSubmitPlanCoverageGuardAcceptsSnippetAlreadyPresentOrPlanned(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("old behavior\nnew regression coverage\nfooter\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.txt"},
			RequiredEditSnippets: []workerruntime.RequiredEditSnippet{{
				ID:      "required_edit_1",
				Path:    "a.txt",
				Content: "old behavior\nnew regression coverage",
			}},
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "footer", NewText: "footer updated"},
		},
	})
	if got.Intercepted {
		t.Fatalf("already-present required snippet should satisfy coverage, got %#v", got)
	}

	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("old behavior\n"), 0o644); err != nil {
		t.Fatalf("reset a.txt: %v", err)
	}
	got = runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "old behavior", NewText: "old behavior\nnew regression coverage"},
		},
	})
	if got.Intercepted {
		t.Fatalf("planned required snippet should satisfy coverage, got %#v", got)
	}
}

func TestPlanThenExecuteSubmitPlanCoverageGuardRejectsPlanThatRemovesRequiredSnippet(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("old behavior\nnew regression coverage\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.txt"},
			RequiredEditSnippets: []workerruntime.RequiredEditSnippet{{
				ID:      "required_edit_1",
				Path:    "a.txt",
				Content: "old behavior\nnew regression coverage",
			}},
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "new regression coverage\n", NewText: ""},
		},
	})
	if !got.Intercepted {
		t.Fatalf("plan removing a required final-state snippet must be rejected")
	}
}

func TestPlanThenExecuteSubmitPlanCoverageGuardRejectsMissingOldTextAnchor(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("package main\nfunc actual() {}\n"), 0o644); err != nil {
		t.Fatalf("seed a.txt: %v", err)
	}
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.txt"},
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{{
			Path:    "a.txt",
			OldText: "func actual lines 2-2 tags: core",
			NewText: "func replacement() {}",
		}},
	})
	if !got.Intercepted {
		t.Fatalf("plan with old_text absent from current source must be rejected")
	}
	if !strings.Contains(got.Result.Error, "old_text not found") {
		t.Fatalf("rejection should explain missing old_text anchor, got %q", got.Result.Error)
	}
}

func TestPlanThenExecuteSubmitPlanCoverageGuardInactiveOutsidePlannerPhase(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{Path: "a.txt", OldText: "a", NewText: "A"},
		},
	})
	if got.Intercepted {
		t.Fatalf("coverage guard must stand down outside planner phase, got %#v", got)
	}
}

func TestPlanThenExecuteWriteGuardLetsReadsThrough(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
		},
	}
	for _, tool := range []string{"read_file", "list_files", "search_files", "search_content", "code_navigate"} {
		got := runner.preflightPlanThenExecuteWriteGuard(workspace.ToolRequest{Tool: tool, Path: "a.txt"})
		if got.Intercepted {
			t.Fatalf("read-class %q must pass through during planner phase, got %#v", tool, got)
		}
	}
}

func TestPlanThenExecuteRequiredPathDiscoveryRejectsBroadReadBeforeRequiredSource(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/productworkflow/coverage_gate.go"},
	}
	summary, errorText, reject := planThenExecuteRequiredPathDiscoveryRejection(workspace.ToolRequest{
		Tool:            "context_query",
		Query:           "what calls productWorkflow",
		Resolution:      "symbol",
		IncludeEvidence: true,
	}, brief, nil)
	if !reject {
		t.Fatalf("broad/semantic discovery should be rejected until required source is read")
	}
	if summary != "Required edit path source read required" {
		t.Fatalf("unexpected summary: %q", summary)
	}
	if !strings.Contains(errorText, "cmd/idiom/product_workflow.go") {
		t.Fatalf("rejection should point at first unread required path, got %q", errorText)
	}
}

func TestPlanThenExecuteRequiredPathDiscoveryAllowsUnreadRequiredPathRead(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/productworkflow/coverage_gate.go"},
	}
	_, _, reject := planThenExecuteRequiredPathDiscoveryRejection(workspace.ToolRequest{
		Tool: "read_file",
		Path: "cmd/idiom/product_workflow.go",
	}, brief, nil)
	if reject {
		t.Fatalf("read_file on an unread required path must be allowed")
	}
}

func TestPlanThenExecuteRequiredPathDiscoveryRejectsAlreadyReadPathUntilRemainingRead(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/productworkflow/coverage_gate.go"},
	}
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n\n1515:func runProductTDD() {}\n",
	}}
	_, errorText, reject := planThenExecuteRequiredPathDiscoveryRejection(workspace.ToolRequest{
		Tool:       "code_navigate",
		Path:       "cmd/idiom/product_workflow.go",
		Capability: "read_symbol",
		Pattern:    "runProductTDD",
	}, brief, results)
	if !reject {
		t.Fatalf("repeat discovery on an already-read required path should be rejected while another required path is unread")
	}
	if !strings.Contains(errorText, "internal/productworkflow/coverage_gate.go") {
		t.Fatalf("rejection should point at remaining unread required path, got %q", errorText)
	}
}

func TestPlanThenExecuteRequiredPathDiscoveryNamesSourceBodyRouteAfterRedirect(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go"},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n\n1515:func runProductTDD() {}\n",
		},
		{
			Tool:    "read_file",
			Summary: "Redirected cmd/idiom/product_workflow_test.go to code_navigate",
			Output:  `candidate_request: {"tool":"code_navigate","capability":"file_outline","path":"cmd/idiom/product_workflow_test.go","language":"go"}`,
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed file_outline via go_file_outline",
			Output:  "route_summary: Outlined 33 top-level declaration(s) in cmd/idiom/product_workflow_test.go.\n",
		},
	}

	_, errorText, reject := planThenExecuteRequiredPathDiscoveryRejection(workspace.ToolRequest{
		Tool:       "code_navigate",
		Path:       "cmd/idiom/product_workflow.go",
		Capability: "read_symbol",
		Pattern:    "runProductTDD",
	}, brief, results)
	if !reject {
		t.Fatalf("repeat discovery on an already-read required path should be rejected while redirected path lacks source-body evidence")
	}
	for _, want := range []string{
		"cmd/idiom/product_workflow_test.go",
		"code_navigate",
		"read_symbol",
		"start_line",
		"Do not repeat whole-file `read_file`",
	} {
		if !strings.Contains(errorText, want) {
			t.Fatalf("rejection missing %q:\n%s", want, errorText)
		}
	}
}

func TestPlanThenExecuteRequiredPathDiscoveryDoesNotCountFileOutlineAsSourceRead(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/productworkflow/coverage_gate.go"},
	}
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed file_outline via go_file_outline",
		Output:  "route_summary: Outlined 65 top-level declaration(s) in cmd/idiom/product_workflow.go. Includes declaration tags and suggested move batches in the output.\n",
	}}
	_, errorText, reject := planThenExecuteRequiredPathDiscoveryRejection(workspace.ToolRequest{
		Tool:       "code_navigate",
		Path:       "cmd/idiom/product_workflow.go",
		Capability: "read_symbol",
		Pattern:    "runProductTDD",
	}, brief, results)
	if reject {
		t.Fatalf("file_outline should orient but not satisfy source-read coverage; follow-up source read should remain allowed: %s", errorText)
	}
}

func TestPlanThenExecuteRequiredPathDiscoveryAllowsBoundedInvalidAnchorRefreshBeforeUnreadPath(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/productworkflow/coverage_gate.go"},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n\n1515:func runProductTDD() {}\n",
		},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "invalid edit anchor(s): `cmd/idiom/product_workflow.go: old_text not found in current source`; missing required edit path(s): `internal/productworkflow/coverage_gate.go`.",
		},
	}

	_, _, reject := planThenExecuteRequiredPathDiscoveryRejection(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "cmd/idiom/product_workflow.go",
		StartLine: 1515,
		EndLine:   1640,
	}, brief, results)
	if reject {
		t.Fatalf("bounded source-body refresh for a rejected invalid anchor should be allowed before the remaining path is read")
	}
}

func TestPlanThenExecuteRejectedPlanSourceRefreshAllowedForInvalidAnchor(t *testing.T) {
	required := []string{"cmd/idiom/product_workflow.go", "internal/productworkflow/coverage_gate.go"}
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n\n1515:func runProductTDD() {}\n",
		},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "invalid edit anchor(s): `cmd/idiom/product_workflow.go: old_text not found in current source`; missing required edit path(s): `internal/productworkflow/coverage_gate.go`.",
		},
	}

	if !planThenExecuteRejectedPlanSourceRefreshAllowed(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "cmd/idiom/product_workflow.go",
		StartLine: 1515,
		EndLine:   1640,
	}, required, results) {
		t.Fatal("bounded source-body refresh for invalid submit_plan anchor should be allowed")
	}
	if planThenExecuteRejectedPlanSourceRefreshAllowed(workspace.ToolRequest{
		Tool: "read_file",
		Path: "cmd/idiom/product_workflow.go",
	}, required, results) {
		t.Fatal("unbounded file refresh should not bypass submit_plan-only recovery")
	}
	if planThenExecuteRejectedPlanSourceRefreshAllowed(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "internal/productworkflow/other.go",
		StartLine: 1,
		EndLine:   20,
	}, required, results) {
		t.Fatal("refresh should stay inside rejected required paths")
	}
}

func TestPlanThenExecuteRejectedPlanSourceRefreshAllowedForPreflightSyntaxValidation(t *testing.T) {
	required := []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go"}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 cmd/idiom/product_workflow.go lines 80-125",
			Output:  "80:type productValidateArtifactOutput struct {}\n100:func runProductValidateArtifact() {}\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read f2 cmd/idiom/product_workflow_test.go lines 1-80",
			Output:  "1:package main\n",
		},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "edits[0]: preflight syntax validation failed for cmd/idiom/product_workflow.go at line 100: ./cmd/idiom/product_workflow.go:100:6: expected '(', found runProductValidateArtifact",
		},
	}

	if !planThenExecuteRejectedPlanSourceRefreshAllowed(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "cmd/idiom/product_workflow.go",
		StartLine: 80,
		EndLine:   125,
	}, required, results) {
		t.Fatal("bounded source-body refresh for submit_plan syntax preflight failure should be allowed")
	}
	if planThenExecuteRejectedPlanSourceRefreshAllowed(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "cmd/idiom/product_workflow_test.go",
		StartLine: 1,
		EndLine:   80,
	}, required, results) {
		t.Fatal("syntax preflight refresh should stay on the path named by the failed plan")
	}
	note := planThenExecutePostSubmitRejectionRefreshNote(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: required,
	}, results, results[len(results)-1], len(results)-1)
	if !strings.Contains(note, "cmd/idiom/product_workflow.go") || strings.Contains(note, "No further read") {
		t.Fatalf("refresh note should name syntax-failed path, got:\n%s", note)
	}
}

func TestPlanThenExecuteCandidateRejectedPlanRefreshRedirectsToSyntaxLine(t *testing.T) {
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
			Output:  `{"candidate_files":[{"path":"internal/productworkflow/coverage_gate.go"}]}`,
		},
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/productworkflow/coverage_gate.go lines 1-391 (total lines: 391)",
			Output:  "1:package productworkflow\n",
		},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "edits[1]: preflight syntax validation failed for internal/productworkflow/coverage_gate.go at line 125: ./internal/productworkflow/coverage_gate.go:125:6: expected '(', found appendAuditBlockers",
		},
	}
	req := workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "internal/productworkflow/coverage_gate.go",
		StartLine: 1,
		EndLine:   50,
	}

	redirected, _, ok := runner.planThenExecuteRejectedPlanRefreshReadRedirect(req, results)
	if !ok {
		t.Fatal("expected rejected-plan refresh read to redirect to syntax failure region")
	}
	if redirected.Path != "internal/productworkflow/coverage_gate.go" || redirected.StartLine > 125 || redirected.EndLine < 125 {
		t.Fatalf("redirected read should cover syntax failure line 125, got %#v", redirected)
	}
	if !runner.planThenExecuteCandidateSourceRefreshAllowed(redirected, results, 0) {
		t.Fatalf("redirected candidate refresh should be allowed after rejected submit_plan")
	}
}

func TestPlanThenExecuteRequiredPathDiscoveryRejectsUnboundedInvalidAnchorRefresh(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/productworkflow/coverage_gate.go"},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n\n1515:func runProductTDD() {}\n",
		},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "invalid edit anchor(s): `cmd/idiom/product_workflow.go: old_text not found in current source`; missing required edit path(s): `internal/productworkflow/coverage_gate.go`.",
		},
	}

	summary, errorText, reject := planThenExecuteRequiredPathDiscoveryRejection(workspace.ToolRequest{
		Tool: "read_file",
		Path: "cmd/idiom/product_workflow.go",
	}, brief, results)
	if !reject {
		t.Fatalf("unbounded invalid-anchor refresh should be rejected because it can redirect without source-body evidence")
	}
	if summary != "Bounded submit_plan anchor refresh required" {
		t.Fatalf("unexpected summary: %q", summary)
	}
	for _, want := range []string{"bounded `read_file`", "read_symbol", "internal/productworkflow/coverage_gate.go"} {
		if !strings.Contains(errorText, want) {
			t.Fatalf("rejection missing %q:\n%s", want, errorText)
		}
	}
}

func TestPlanThenExecutePostReadinessDiscoveryRejectsMoreSearch(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/productworkflow/coverage_gate.go"},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n\n1515:func runProductTDD() {}\n",
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read EvaluateCoverageGate in internal/productworkflow/coverage_gate.go lines 1-120\n\n1:func EvaluateCoverageGate() {}\n",
		},
	}
	summary, errorText, reject := planThenExecutePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:    "search_content",
		Path:    "cmd/idiom/product_workflow.go",
		Pattern: "coverage gate",
	}, brief, results)
	if !reject {
		t.Fatal("post-readiness read/search should be rejected")
	}
	if summary != "Plan submission required" {
		t.Fatalf("unexpected summary: %q", summary)
	}
	if !strings.Contains(errorText, "Submit one parser-readable `submit_plan`") {
		t.Fatalf("rejection should force plan submission, got %q", errorText)
	}
}

func TestPlanThenExecutePostReadinessDiscoveryRejectsAfterBadSubmitPlan(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/productworkflow/coverage_gate.go"},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n\n1515:func runProductTDD() {}\n",
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read EvaluateCoverageGate in internal/productworkflow/coverage_gate.go lines 1-120\n\n1:func EvaluateCoverageGate() {}\n",
		},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "submit_plan must include at least one edit entry for every required_edit_paths entry; missing required edit path(s): `internal/productworkflow/coverage_gate.go`; invalid edit anchor(s): `cmd/idiom/product_workflow.go: old_text not found in current source`.",
		},
	}
	summary, errorText, reject := planThenExecutePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:    "search_content",
		Path:    "cmd/idiom/product_workflow.go",
		Pattern: "runProductTDD",
	}, brief, results)
	if !reject {
		t.Fatal("post-submit-plan recovery search should be rejected")
	}
	if summary != "Corrected submit_plan required" {
		t.Fatalf("unexpected summary: %q", summary)
	}
	if !strings.Contains(errorText, "previous `submit_plan` was rejected before it became a valid plan artifact") || !strings.Contains(errorText, "only remaining read exception") {
		t.Fatalf("rejection should point back to corrected submit_plan, got %q", errorText)
	}
	if !strings.Contains(errorText, "source-body obligation") || !strings.Contains(errorText, "`edits[]` coverage") {
		t.Fatalf("rejection should distinguish source evidence from plan coverage, got %q", errorText)
	}
	if !strings.Contains(errorText, "Last submit_plan rejection:") || !strings.Contains(errorText, "missing required edit path") {
		t.Fatalf("rejection should preserve exact submit_plan failure details, got %q", errorText)
	}
	_, _, reject = planThenExecutePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool: "read_file",
		Path: "cmd/idiom/product_workflow.go",
	}, brief, results)
	if !reject {
		t.Fatal("unbounded read_file on the stale required-path anchor should be rejected")
	}
	_, _, reject = planThenExecutePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "cmd/idiom/product_workflow.go",
		StartLine: 1515,
		EndLine:   1640,
	}, brief, results)
	if reject {
		t.Fatal("one bounded read_file on the stale required-path anchor should be allowed")
	}
	_, _, reject = planThenExecutePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:       "code_navigate",
		Path:       "cmd/idiom/product_workflow.go",
		Capability: "read_symbol",
		Pattern:    "runProductTDD",
	}, brief, results)
	if reject {
		t.Fatal("one source-body code_navigate read on the stale required-path anchor should be allowed")
	}
	_, _, reject = planThenExecutePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool: "read_file",
		Path: "internal/productworkflow/coverage_gate.go",
	}, brief, results)
	if !reject {
		t.Fatal("unbounded read_file on a missing required plan path should be rejected")
	}
	_, _, reject = planThenExecutePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "internal/productworkflow/coverage_gate.go",
		StartLine: 1,
		EndLine:   120,
	}, brief, results)
	if !reject {
		t.Fatal("bounded read_file on a missing required plan path should be rejected when prior source-body evidence already exists")
	}
	outlineOnlyMissingPathResults := []workspace.ToolResult{
		results[0],
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed file_outline via go_file_outline",
			Output:  "route_summary: Outlined 3 top-level declaration(s) in internal/productworkflow/coverage_gate.go.\n",
		},
		results[2],
	}
	_, _, reject = planThenExecutePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "internal/productworkflow/coverage_gate.go",
		StartLine: 1,
		EndLine:   120,
	}, brief, outlineOnlyMissingPathResults)
	if reject {
		t.Fatal("one bounded read_file on a missing required plan path with only outline evidence should be allowed")
	}
	withRefresh := append(append([]workspace.ToolResult(nil), results...), workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read cmd/idiom/product_workflow.go lines 1515-1640",
	})
	_, errorText, reject = planThenExecutePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "cmd/idiom/product_workflow.go",
		StartLine: 1515,
		EndLine:   1640,
	}, brief, withRefresh)
	if !reject {
		t.Fatal("second bounded source-body read on same stale anchor should be rejected")
	}
	if !strings.Contains(errorText, "No further read/search/discovery action is currently allowed") {
		t.Fatalf("second anchor refresh should clearly require corrected submit_plan, got %q", errorText)
	}
}

func TestPlanThenExecutePostReadinessRecoverySkipsMissingPlanPathRefreshWithSourceBodyEvidence(t *testing.T) {
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
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/productworkflow/coverage_gate.go"},
	}, []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 cmd/idiom/product_workflow.go lines 1-20"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read EvaluateCoverageGate in internal/productworkflow/coverage_gate.go lines 1-80"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "missing required edit path(s): `internal/productworkflow/coverage_gate.go`.",
		},
	})

	if opts.ToolChoice != "required" {
		t.Fatalf("source-backed missing-path recovery should force submit_plan tool choice, got %q", opts.ToolChoice)
	}
	if len(opts.Tools) != 1 || !chatOptionsExposeTool(opts, "submit_plan") || chatOptionsExposeTool(opts, "read_file") || chatOptionsExposeTool(opts, "code_navigate") {
		t.Fatalf("source-backed missing-path recovery should expose only submit_plan, got %#v", opts.Tools)
	}
	if opts.EnableThinking == nil || *opts.EnableThinking {
		t.Fatalf("source-backed missing-path recovery should force visible corrected submit_plan output, got %#v", opts.EnableThinking)
	}
	if !opts.ChatTemplateKwargs {
		t.Fatalf("source-backed missing-path recovery should force request-level thinking kwargs")
	}
}

func TestPlanThenExecutePostReadinessRecoveryAllowsOutlineOnlyMissingPlanPathRefresh(t *testing.T) {
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
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/productworkflow/coverage_gate.go"},
	}, []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 cmd/idiom/product_workflow.go lines 1-20"},
		{Tool: "code_navigate", Summary: "Code navigation routed file_outline via go_file_outline", Output: "route_summary: Outlined 3 top-level declaration(s) in internal/productworkflow/coverage_gate.go."},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "missing required edit path(s): `internal/productworkflow/coverage_gate.go`.",
		},
	})

	if opts.ToolChoice != "required" {
		t.Fatalf("outline-only missing-path recovery should force a parser-readable tool request, got %q", opts.ToolChoice)
	}
	if len(opts.Tools) != 2 || !chatOptionsExposeTool(opts, "read_file") || !chatOptionsExposeTool(opts, "code_navigate") || chatOptionsExposeTool(opts, "search_content") || chatOptionsExposeTool(opts, "submit_plan") {
		t.Fatalf("outline-only missing-path recovery should expose only source-body read tools, got %#v", opts.Tools)
	}
	if opts.EnableThinking == nil || *opts.EnableThinking {
		t.Fatalf("outline-only missing-path recovery should force short source-read output, got %#v", opts.EnableThinking)
	}
}

func TestPlanThenExecuteRequiredPathDiscoveryRejectsOutOfScopeInvalidAnchorRefresh(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/productworkflow/coverage_gate.go"},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n\n1515:func runProductTDD() {}\n",
		},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "invalid edit anchor(s): `docs/out-of-scope.md: old_text not found in current source`; path(s) outside required_edit_paths: `docs/out-of-scope.md`.",
		},
	}

	_, errorText, reject := planThenExecuteRequiredPathDiscoveryRejection(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "docs/out-of-scope.md",
		StartLine: 1,
		EndLine:   20,
	}, brief, results)
	if !reject {
		t.Fatal("invalid-anchor recovery must not unlock out-of-scope reads")
	}
	if !strings.Contains(errorText, "outside required_edit_paths") {
		t.Fatalf("rejection should explain out-of-scope anchor, got %q", errorText)
	}
}

func TestPlanThenExecutePostReadinessDiscoveryAllowsUnreadPathRead(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/productworkflow/coverage_gate.go"},
	}
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n\n1515:func runProductTDD() {}\n",
	}}
	_, _, reject := planThenExecutePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool: "read_file",
		Path: "internal/productworkflow/coverage_gate.go",
	}, brief, results)
	if reject {
		t.Fatal("required path reads should remain allowed before planner readiness")
	}
}

func TestPlanThenExecutePlannerToolGuardRejectsShellAndVerification(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
		},
	}
	for _, tool := range []string{"shell", "git_diff", "git_status", "source_sanity", "affected_suites", "context_trace_pack", "task", "task_ledger", "pwd"} {
		got := runner.preflightPlanThenExecutePlannerToolGuard(workspace.ToolRequest{Tool: tool})
		if !got.Intercepted {
			t.Fatalf("planner phase must reject %q, got %#v", tool, got)
		}
		if !strings.Contains(got.Result.Error, "source-satisfied") {
			t.Fatalf("rejection should name the source-satisfied closeout, got %q", got.Result.Error)
		}
	}
}

func TestPlanThenExecuteClampedEOFReadRejectsRepeatedOutOfRangeWindow(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f4 cmd/idiom/product_workflow.go lines 2705-2805 (requested range 3700-3800 was clamped to file bounds; file has 2805 lines total)",
		Output:  "2705:tail",
	}}
	summary, errorText, reject := runner.planThenExecuteClampedEOFReadRejection(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "cmd/idiom/product_workflow.go",
		StartLine: 3800,
		EndLine:   3900,
	}, results)
	if !reject {
		t.Fatal("expected repeated read_file beyond known EOF to be rejected")
	}
	if summary != "Out-of-range read_file rejected" || !strings.Contains(errorText, "submit_plan") || !strings.Contains(errorText, "2805 lines total") {
		t.Fatalf("rejection should explain the EOF fact and valid planner closeouts, summary=%q error=%q", summary, errorText)
	}
}

func TestPlanThenExecuteClampedEOFReadAllowsUsefulBoundedWindow(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read target.txt lines 1-3 (requested range 1-30 was clamped to file bounds; file has 3 lines total)",
		Output:  "1:a",
	}}
	_, _, reject := runner.planThenExecuteClampedEOFReadRejection(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "target.txt",
		StartLine: 2,
		EndLine:   3,
	}, results)
	if reject {
		t.Fatal("a bounded read that starts before known EOF can still add evidence")
	}
}

func TestPlanThenExecuteHardRedirectDoesNotHideReadFileWithRequiredAnchors(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"cmd/idiom/product_workflow.go"},
		},
	}
	if runner.shouldHideToolAfterDiscoveryHardRedirect(workspace.ToolRequest{
		Tool: "read_file",
		Path: "cmd/idiom/main.go",
	}) {
		t.Fatal("plan-execute planner must keep read_file available for later exact-anchor reads on required edit paths")
	}
	if !runner.shouldHideToolAfterDiscoveryHardRedirect(workspace.ToolRequest{
		Tool: "search_content",
		Path: "cmd/idiom/main.go",
	}) {
		t.Fatal("non-anchor read tools can still be hidden after hard redirect")
	}
}

func TestPlanThenExecutePlannerToolGuardDoesNotAffectReviewRepair(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecuteReviewRepairTaskFamily,
		},
	}
	if got := runner.preflightPlanThenExecutePlannerToolGuard(workspace.ToolRequest{Tool: "git_diff"}); got.Intercepted {
		t.Fatalf("review-repair may inspect diff evidence; planner-only guard must stand down, got %#v", got)
	}
}

func TestReviewRepairCompletionGateRejectsNonDecisionFinalAnswer(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
			AllowedTools:      []string{"submit_plan"},
		},
	}
	got, rejected := runner.completionGateRejection("Let me trace the actual implementation to verify the tests against real code.", nil, 1, nil, false)
	if !rejected {
		t.Fatal("review-repair must reject process narration as a final answer")
	}
	if got.Tool != "completion_gate" || !strings.Contains(got.Error, "Decision: pass") || !strings.Contains(got.Error, "submit_plan") {
		t.Fatalf("review-repair rejection should name the valid closeout shapes, got %#v", got)
	}
}

func TestReviewRepairCompletionGateAcceptsPassChecklistOrSubmittedPlan(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
			AllowedTools:      []string{"submit_plan"},
		},
	}
	passAnswer := "Decision: pass\n\nVerification Checklist:\n- Required behavior is satisfied.\n- Scope safety checked."
	if got, rejected := runner.completionGateRejection(passAnswer, nil, 1, nil, false); rejected {
		t.Fatalf("review-repair pass checklist should be accepted, got %#v", got)
	}
	pathOnlyChecklist := "Decision: pass\n\nVerification Checklist:\n- `widget_config.json`\n- `widget_notes.md`\n"
	if got, rejected := runner.completionGateRejection(pathOnlyChecklist, nil, 1, nil, false); rejected {
		t.Fatalf("review-repair pass checklist must not be rejected by the generic read-only explanation gate, got %#v", got)
	}
	recoveryTemplate := "The completion gate rejected the review-repair response.\n\nPass shape:\nDecision: pass\n\nVerification Checklist:\n- Required behavior: <checked fact>."
	if got, rejected := runner.completionGateRejection(recoveryTemplate, nil, 1, nil, false); !rejected {
		t.Fatalf("review-repair must not accept recovery template text as a pass, got %#v", got)
	}
	results := []workspace.ToolResult{{Tool: "submit_plan", Summary: "Plan submitted with 1 edit(s); the executor phase will dispatch them."}}
	if got, rejected := runner.completionGateRejection("Plan submitted with 1 edit.", results, 2, nil, false); rejected {
		t.Fatalf("review-repair submitted corrective plan should be accepted, got %#v", got)
	}
}

func TestReviewRepairCompletionGateRejectsPassWhenPhaseEvidenceFailed(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
			Readiness: &workerruntime.ReadinessState{
				OpenObligations: []string{"verification_failed:go test ./..."},
			},
		},
	}
	passAnswer := "Decision: pass\n\nVerification Checklist:\n- Required behavior is satisfied.\n- Test evidence passed."
	got, rejected := runner.completionGateRejection(passAnswer, nil, 1, nil, false)
	if !rejected {
		t.Fatal("review-repair pass must be rejected while phase evidence has failed verification")
	}
	if got.Tool != "completion_gate" || !strings.Contains(got.Error, "verification_failed:go test ./...") || !strings.Contains(got.Error, "submit_plan") {
		t.Fatalf("rejection should name failed obligation and corrective plan path, got %#v", got)
	}
	if got, rejected := runner.completionGateRejection("<<QUESTION>>: verification failed outside allowed repair paths", nil, 1, nil, false); rejected {
		t.Fatalf("typed blocker should remain accepted with failed phase evidence, got %#v", got)
	}
	results := []workspace.ToolResult{{Tool: "submit_plan", Summary: "Plan submitted with 1 edit(s); the executor phase will dispatch them."}}
	if got, rejected := runner.completionGateRejection("Plan submitted with 1 edit.", results, 2, nil, false); rejected {
		t.Fatalf("corrective submit_plan should remain accepted with failed phase evidence, got %#v", got)
	}
}

func TestReviewRepairDoesNotTriggerReadOnlyAnswerReady(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read cmd/idiom/product_workflow.go lines 280-360 (total lines: 2961)",
			Output:  "280:func runProductLint() error { return nil }",
		},
		{
			Tool:    "read_file",
			Summary: "Read cmd/idiom/product_workflow.go lines 1-50 (total lines: 2961)",
			Output:  "1:package main",
		},
	}
	if runner.readOnlyAnalysisAnswerReadyAfterResult(workspace.ToolRequest{Tool: "read_file"}, results[1], results) {
		t.Fatal("review-repair is a plan-submission phase and must not receive generic read-only answer-ready nudges")
	}
}

func TestReviewRepairBroadReadGuardRejectsLargeRawReads(t *testing.T) {
	root := t.TempDir()
	var b strings.Builder
	for i := 0; i < planThenExecuteReviewRepairMaxRawReadLines+20; i++ {
		fmt.Fprintf(&b, "line %03d\n", i+1)
	}
	if err := os.WriteFile(filepath.Join(root, "large_test.go"), []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
		},
	}
	if got := runner.preflightPlanThenExecuteReviewRepairBroadReadGuard(workspace.ToolRequest{
		Tool: "read_file",
		Path: "large_test.go",
	}); !got.Intercepted || !strings.Contains(got.Result.Error, "Phase Evidence") || !strings.Contains(got.Result.Error, "context_status") {
		t.Fatalf("review-repair must reject unbounded large raw reads with evidence-led guidance, got %#v", got)
	}
	if got := runner.preflightPlanThenExecuteReviewRepairBroadReadGuard(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "large_test.go",
		StartLine: 1,
		EndLine:   planThenExecuteReviewRepairMaxRawReadLines + 1,
	}); !got.Intercepted || !strings.Contains(got.Result.Error, "at most 200 lines") {
		t.Fatalf("review-repair must reject overly broad explicit ranges, got %#v", got)
	}
}

func TestReviewRepairBroadReadGuardAllowsNarrowRanges(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "large_test.go"), []byte(strings.Repeat("x\n", 500)), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
		},
	}
	if got := runner.preflightPlanThenExecuteReviewRepairBroadReadGuard(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "large_test.go",
		StartLine: 25,
		EndLine:   50,
	}); got.Intercepted {
		t.Fatalf("review-repair should allow one narrow source range, got %#v", got)
	}
}

func TestReviewRepairBroadReadGuardDoesNotAffectPlanner(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "large_test.go"), []byte(strings.Repeat("x\n", 500)), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
		},
	}
	if got := runner.preflightPlanThenExecuteReviewRepairBroadReadGuard(workspace.ToolRequest{
		Tool: "read_file",
		Path: "large_test.go",
	}); got.Intercepted {
		t.Fatalf("review-repair broad read guard must not change planner read policy, got %#v", got)
	}
}

func TestReviewRepairCompletionGateRecoveryUsesEvidenceLedgerNotBroadReads(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
		},
	}
	prompt, hideReads, ok := runner.completionGateRecoveryPrompt([]workspace.ToolResult{
		{
			Tool:    "completion_gate",
			Summary: "Review-repair completion gate rejected final answer",
			Error:   "review-repair must close with exactly one of: Decision: pass, submit_plan, or <<QUESTION>>",
		},
	})
	if !ok {
		t.Fatal("review-repair completion rejection should produce a phase-specific recovery prompt")
	}
	if !hideReads {
		t.Fatal("review-repair completion recovery should hide read/discovery tools after the closeout gate fires")
	}
	for _, want := range []string{
		"Decision: pass",
		"Verification Checklist",
		"corrective `submit_plan`",
		"<<QUESTION>>",
		"Do not add prose before or after",
		"Phase Evidence Bundle",
		"hidden reasoning",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("review-repair recovery prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, forbidden := range []string{
		"read_file",
		"code_navigate",
		"context_query",
		"context_zoom",
		"context_status",
		"targeted probe",
		"at most 200 lines",
	} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("review-repair terminal recovery must not invite more discovery via %q:\n%s", forbidden, prompt)
		}
	}
	if strings.Contains(prompt, "produce the missing evidence or edit required by the gate") {
		t.Fatalf("review-repair recovery should not use generic completion-gate wording:\n%s", prompt)
	}
}

func TestReviewRepairCompletionGateRecoveryWithFailedEvidenceSuppressesPass(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
			Readiness: &workerruntime.ReadinessState{
				OpenObligations: []string{"verification_failed:go test ./..."},
			},
		},
	}
	prompt, hideReads, ok := runner.completionGateRecoveryPrompt([]workspace.ToolResult{
		{
			Tool:    "completion_gate",
			Summary: "Review-repair completion gate rejected final answer",
			Error:   "review-repair must close with exactly one terminal outcome",
		},
	})
	if !ok {
		t.Fatal("review-repair completion rejection should produce a phase-specific recovery prompt")
	}
	if !hideReads {
		t.Fatal("review-repair completion recovery should hide read/discovery tools after the closeout gate fires")
	}
	for _, want := range []string{
		"`Decision: pass` is invalid",
		"verification_failed:go test ./...",
		"corrective `submit_plan`",
		"<<QUESTION>>",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("review-repair failed-evidence prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, forbidden := range []string{
		"`Decision: pass` with a `Verification Checklist`",
		"Pass: `Decision: pass`",
	} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("review-repair failed-evidence prompt must not advertise pass shape %q:\n%s", forbidden, prompt)
		}
	}
}

func TestReviewRepairCompletionGateRecoveryForcesVisibleOutput(t *testing.T) {
	thinkingOn := true
	opts := completionGateRecoveryChatOptions(llama.ChatOptions{
		EnableThinking:   &thinkingOn,
		PreserveThinking: true,
		MaxTokens:        8192,
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
		Contract:          "review_findings",
		PermissionProfile: "plan_submission_review",
	}, nil)
	if opts.EnableThinking == nil || *opts.EnableThinking {
		t.Fatalf("review-repair recovery should force visible output over hidden thinking, got %#v", opts.EnableThinking)
	}
	if opts.PreserveThinking {
		t.Fatal("review-repair recovery must not preserve hidden reasoning in the next request")
	}
	if opts.MaxTokens != 8192 {
		t.Fatalf("review-repair recovery should preserve corrective-plan budget, got %d", opts.MaxTokens)
	}
}

func TestReviewRepairTerminalRecoveryRejectsReadsWithoutGenericWriteOnlyBlocker(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "coverage_gate.go"), []byte("package p\n\nfunc Check() bool { return true }\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	replies := []string{
		"I will inspect the source before deciding.",
		`{"tool":"code_navigate","capability":"read_symbol","path":"coverage_gate.go","pattern":"Check"}`,
		"Decision: pass\n\nVerification Checklist:\n- Existing phase evidence is sufficient.\n- No corrective plan is required.",
	}
	var requests int
	var sawTerminalRequired bool
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
		Policy: RunPolicy{MaxToolSteps: 5, MaxNoProgressTurns: 5},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "plan_then_execute_review_repair_terminal_required" {
				sawTerminalRequired = true
			}
			if strings.Contains(event.Content, "Write-only recovery blocked non-write tool") {
				t.Fatalf("review-repair terminal recovery must not fall through to generic write-only blocker: %s", event.Content)
			}
			if event.Role == "next_action_state" || event.Role == "failure_feedback" {
				forbidden := []string{
					"answer from the current evidence if it already resolves the task",
					"otherwise take one exact tool-shaped follow-up",
				}
				for _, text := range forbidden {
					if strings.Contains(event.Content, text) {
						t.Fatalf("review-repair terminal recovery must not advertise generic follow-up %q in %s: %s", text, event.Role, event.Content)
					}
				}
			}
		},
	}
	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "review the applied plan"}}, llama.ChatOptions{MaxTokens: 2048}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if requests != len(replies) {
		t.Fatalf("expected recovery to continue through terminal pass, got %d request(s)", requests)
	}
	if !sawTerminalRequired {
		t.Fatalf("expected review-repair terminal recovery event")
	}
	if !strings.Contains(answer, "Decision: pass") {
		t.Fatalf("expected terminal pass answer, got:\n%s", answer)
	}
}

func TestReviewRepairTerminalProbeUsesPhaseEvidencePassWithoutRejection(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "internal_labels.go"), []byte("package labels\n\nfunc ShortLabel() string { return \"Save\" }\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var requests int
	var sawTerminalRequired bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests > 0 {
			t.Fatalf("unexpected extra chat request")
		}
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": `{"tool":"read_file","path":"internal/labels/labels.go","start_line":1,"end_line":20}`},
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
		Policy: RunPolicy{MaxToolSteps: 5, MaxNoProgressTurns: 5},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
			ChangedPaths:      []string{"internal/labels/labels.go"},
			AllowedEditPaths:  []string{"internal/labels/labels.go"},
			Objective: strings.Join([]string{
				"schema: idiom.phase_evidence.v1",
				"phase: review_repair",
				"submitted_plan_state:",
				"- `internal/labels/labels.go` status=applied summary=edit 1/1: submitted new_text is present error=",
				"source_sanity:",
				"- `internal/labels/labels.go` status=ok summary=Source sanity passed for 1 file(s) error=",
				"verification:",
				"- `go test ./...` status=ok summary=ok example.com/frozen-label-repo/internal/labels error=",
				"patch_hygiene: status=passed",
			}, "\n"),
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "plan_then_execute_review_repair_terminal_required" {
				sawTerminalRequired = true
			}
		},
	}
	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "review the applied plan"}}, llama.ChatOptions{MaxTokens: 2048}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if requests != 1 {
		t.Fatalf("expected deterministic pass after the first hidden probe, got %d requests", requests)
	}
	if sawTerminalRequired {
		t.Fatal("positive phase evidence should close without a terminal-required rejection")
	}
	if !strings.Contains(answer, "Decision: pass") || !strings.Contains(answer, "Verification Checklist") {
		t.Fatalf("expected phase-evidence pass answer, got:\n%s", answer)
	}
}

func TestReviewRepairTerminalRecoveryStopsRepeatedHiddenDiscovery(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "coverage_gate.go"), []byte("package p\n\nfunc Check() bool { return true }\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	replies := []string{
		"I will inspect the source before deciding.",
		`{"tool":"code_navigate","capability":"read_symbol","path":"coverage_gate.go","pattern":"Check"}`,
		`{"tool":"context_query","query":"coverage gate callers","resolution":"symbol"}`,
		"Decision: pass\n\nVerification Checklist:\n- This response should not be requested.",
	}
	var requests int
	var terminalRequiredEvents int
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
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "plan_then_execute_review_repair_terminal_required" {
				terminalRequiredEvents++
			}
		},
	}
	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "review the applied plan"}}, llama.ChatOptions{MaxTokens: 2048}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if requests != 3 {
		t.Fatalf("expected runner to stop after repeated hidden discovery, got %d request(s)", requests)
	}
	if terminalRequiredEvents != 2 {
		t.Fatalf("expected two terminal-required events, got %d", terminalRequiredEvents)
	}
	if !strings.Contains(answer, "repeatedly requested hidden discovery after the closeout gate") {
		t.Fatalf("expected deterministic repeated-discovery blocker, got:\n%s", answer)
	}
	if strings.Contains(answer, "This response should not be requested") {
		t.Fatalf("runner requested another completion after repeated hidden discovery:\n%s", answer)
	}
}

func TestReviewRepairTerminalRecoveryAllowsCorrectiveSubmitPlan(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "coverage_gate.go"), []byte("package p\n\nfunc Check() bool { return false }\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	replies := []string{
		"I will inspect the source before deciding.",
		`{"tool":"submit_plan","rationale":"Verification Checklist:\n- Corrective edit is needed for the failed review evidence.","edits":[{"path":"coverage_gate.go","old_text":"return false","new_text":"return true","purpose":"correct the reviewed coverage gate result"}]}`,
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
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
		},
		OnEvent: func(event RunEvent) {
			if strings.Contains(event.Content, "Write-only recovery blocked non-write tool") {
				t.Fatalf("corrective submit_plan must not hit generic write-only blocker: %s", event.Content)
			}
		},
	}
	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "review the applied plan"}}, llama.ChatOptions{MaxTokens: 2048}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if requests != len(replies) {
		t.Fatalf("expected terminal corrective plan after one recovery prompt, got %d request(s)", requests)
	}
	if !strings.Contains(answer, "Plan submitted") {
		t.Fatalf("expected corrective submit_plan to be accepted, got:\n%s", answer)
	}
}

func TestReviewRepairCompletionCloseoutDoesNotInventMissingWrites(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
			RequiredEditPaths: []string{"widget_config.json", "widget_notes.md"},
		},
	}
	answer := runner.completionGateRepeatedBlockerAnswer(workspace.ToolResult{
		Tool:  "completion_gate",
		Error: "review-repair must close with Decision: pass or a corrective plan",
	}, nil)
	if strings.Contains(answer, "Required edit paths not changed") || strings.Contains(answer, "no successful write was recorded") {
		t.Fatalf("review-repair closeout should not describe allowed repair scope as missing writes:\n%s", answer)
	}
	if !strings.Contains(answer, "review-repair must close") {
		t.Fatalf("closeout should preserve the actual completion blocker, got:\n%s", answer)
	}
}

func TestReviewRepairNoopSubmitPlanGuardSuppressesMalformedPassPlan(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
		},
	}
	got := runner.preflightPlanThenExecuteReviewRepairNoopSubmitPlanGuard(workspace.ToolRequest{
		Tool:      "submit_plan",
		Rationale: "Verification Checklist:\n- Required behavior passes.",
		Edits:     []workspace.PlanEdit{{Purpose: "verification pass"}},
	})
	if !got.Intercepted {
		t.Fatal("review-repair malformed/no-path submit_plan should be suppressed before workspace schema errors")
	}
	if got.Result.Tool != "review_repair_noop_plan" || strings.TrimSpace(got.Result.Error) != "" {
		t.Fatalf("noop review plan should be a non-error nudge, got %#v", got.Result)
	}
	if _, ok := planThenExecuteShortCircuitAnswer(runner.RuntimeBrief, workspace.ToolRequest{Tool: "submit_plan"}, got.Result); ok {
		t.Fatal("suppressed review-repair noop plan must not look like a successful corrective submit_plan")
	}
}

func TestReviewRepairNoopSubmitPlanForcesTerminalCloseout(t *testing.T) {
	replies := []string{
		`{"tool":"submit_plan","rationale":"No corrective edits are needed.","edits":[]}`,
		"Decision: pass\n\nVerification Checklist:\n- Required behavior: checked from phase evidence.\n- Scope safety: changed paths are allowed.\n- Test or command evidence: pending outside this lane.",
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
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: t.TempDir(), CWD: "."}),
		Policy:    RunPolicy{MaxToolSteps: 5, MaxNoProgressTurns: 5},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "doom_loop_guard" {
				t.Fatalf("review-repair noop submit_plan must close before doom-loop approval: %s", event.Content)
			}
		},
	}
	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "review the applied plan"}}, llama.ChatOptions{MaxTokens: 2048}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if requests != len(replies) {
		t.Fatalf("expected one forced terminal closeout after noop submit_plan, got %d request(s)", requests)
	}
	if !strings.Contains(answer, "Decision: pass") || !strings.Contains(answer, "Verification Checklist") {
		t.Fatalf("expected terminal review-repair pass, got:\n%s", answer)
	}
}

func TestReviewRepairNoopSubmitPlanPassCloseoutFromChecklistRationale(t *testing.T) {
	answer, ok := reviewRepairNoopSubmitPlanPassCloseout(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
		Contract:          "review_findings",
		PermissionProfile: "plan_submission_review",
		ChangedPaths:      []string{"internal/example/target.go"},
		AllowedEditPaths:  []string{"internal/example/target.go"},
		Objective: strings.Join([]string{
			"phase: review_repair",
			"submitted_plan_state:",
			"- `internal/example/target.go` status=applied summary=edit 1/1: submitted new_text is present error=",
			"source_sanity:",
			"- `internal/example/target.go` status=ok summary=Source sanity passed error=",
			"verification:",
			"- `go test ./...` status=ok summary=ok error=",
			"patch_hygiene: status=passed",
		}, "\n"),
	}, workspace.ToolRequest{
		Tool:      "submit_plan",
		Rationale: "Verification Checklist:\n- Required behavior: checked from phase evidence.\n- Scope safety: changed paths are allowed.\n- Test or command evidence: passed.",
		Edits:     []workspace.PlanEdit{},
	})
	if !ok {
		return
	}
	t.Fatalf("empty submit_plan checklist must remain an invalid wrapper, got:\n%s", answer)
}

func TestReviewRepairNoopSubmitPlanPassCloseoutAcceptsExplicitPassChecklist(t *testing.T) {
	answer, ok := reviewRepairNoopSubmitPlanPassCloseout(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
		Contract:          "review_findings",
		PermissionProfile: "plan_submission_review",
		ChangedPaths:      []string{"internal/labels/labels.go"},
		AllowedEditPaths:  []string{"internal/labels/labels.go"},
		Objective: strings.Join([]string{
			"schema: idiom.phase_evidence.v1",
			"phase: review_repair",
			"submitted_plan_state:",
			"- `internal/labels/labels.go` status=applied summary=edit 1/1: submitted new_text is present error=",
			"source_sanity:",
			"- `internal/labels/labels.go` status=ok summary=Source sanity passed for 1 file(s) error=",
			"verification:",
			"- `go test ./...` status=ok summary=ok example.com/frozen-label-repo/internal/labels error=",
			"patch_hygiene: status=passed",
		}, "\n"),
	}, workspace.ToolRequest{
		Tool: "submit_plan",
		Rationale: strings.Join([]string{
			"Verification Checklist:",
			"- Required behavior: diff evidence shows ShortLabel returns \"Save\".",
			"- Scope safety: changed paths are allowed.",
			"- Test or command evidence: go test ./... passed.",
			"Decision: pass",
		}, "\n"),
		Edits: []workspace.PlanEdit{},
	})
	if !ok {
		t.Fatal("expected explicit pass checklist in no-op submit_plan to close from phase evidence")
	}
	if !strings.HasPrefix(answer, "Decision: pass\n\nVerification Checklist:") {
		t.Fatalf("pass closeout should normalize Decision: pass to the front, got:\n%s", answer)
	}
	if strings.Count(answer, "Decision: pass") != 1 {
		t.Fatalf("pass closeout should not duplicate Decision: pass, got:\n%s", answer)
	}
}

func TestReviewRepairNoopSubmitPlanPassCloseoutRejectsChecklistWithoutPhaseEvidence(t *testing.T) {
	_, ok := reviewRepairNoopSubmitPlanPassCloseout(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
		Contract:          "review_findings",
		PermissionProfile: "plan_submission_review",
	}, workspace.ToolRequest{
		Tool:      "submit_plan",
		Rationale: "Verification Checklist:\n- Required behavior: checked from phase evidence.\n- Scope safety: changed paths are allowed.\n- Test or command evidence: passed.",
		Edits:     []workspace.PlanEdit{},
	})
	if ok {
		t.Fatal("no-op submit_plan checklist must not synthesize pass without explicit phase evidence")
	}
}

func TestReviewRepairNoopSubmitPlanPassCloseoutFromPhaseEvidence(t *testing.T) {
	answer, ok := reviewRepairNoopSubmitPlanPassCloseout(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
		Contract:          "review_findings",
		PermissionProfile: "plan_submission_review",
		ChangedPaths:      []string{"internal/labels/labels.go"},
		AllowedEditPaths:  []string{"internal/labels/labels.go"},
		Objective: strings.Join([]string{
			"schema: idiom.phase_evidence.v1",
			"phase: review_repair",
			"submitted_plan_state:",
			"- `internal/labels/labels.go` status=applied summary=edit 1/1: submitted new_text is present error=",
			"source_sanity:",
			"- `internal/labels/labels.go` status=ok summary=Source sanity passed for 1 file(s) error=",
			"verification:",
			"- `go test ./...` status=ok summary=ok example.com/frozen-label-repo/internal/labels error=",
			"patch_hygiene: status=passed",
		}, "\n"),
	}, workspace.ToolRequest{
		Tool:  "submit_plan",
		Edits: []workspace.PlanEdit{},
	})
	if !ok {
		return
	}
	t.Fatalf("empty submit_plan phase-evidence wrapper must not synthesize pass directly, got:\n%s", answer)
}

func TestReviewRepairNoopSubmitPlanPassCloseoutRejectsFailingChecklistItem(t *testing.T) {
	_, ok := reviewRepairNoopSubmitPlanPassCloseout(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
		Contract:          "review_findings",
		PermissionProfile: "plan_submission_review",
	}, workspace.ToolRequest{
		Tool:      "submit_plan",
		Rationale: "Verification Checklist:\n- Required behavior: checked.\n- Failing item: scope safety still needs a corrective edit.",
		Edits:     []workspace.PlanEdit{},
	})
	if ok {
		t.Fatal("failing checklist item should not synthesize a pass")
	}
}

func TestReviewRepairNoopSubmitPlanPassCloseoutRejectsPlanArtifactEdits(t *testing.T) {
	_, ok := reviewRepairNoopSubmitPlanPassCloseout(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
		Contract:          "review_findings",
		PermissionProfile: "plan_submission_review",
	}, workspace.ToolRequest{
		Tool:      "submit_plan",
		Rationale: "Verification Checklist:\n- Required behavior: checked.\n- Scope safety: changed paths are allowed.\n- Test or command evidence: passed.",
		PlanArtifact: &workspace.PlanArtifact{
			Edits: []workspace.PlanEdit{{Path: "internal/example/target.go", OldText: "old", NewText: "new", Purpose: "fix target"}},
		},
	})
	if ok {
		t.Fatal("structured plan_artifact edits should remain a corrective plan, not synthesize pass")
	}
}

func TestReviewRepairNoopSubmitPlanPassCloseoutRejectsMixedPlanArtifactEdits(t *testing.T) {
	_, ok := reviewRepairNoopSubmitPlanPassCloseout(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
		Contract:          "review_findings",
		PermissionProfile: "plan_submission_review",
	}, workspace.ToolRequest{
		Tool:      "submit_plan",
		Rationale: "Verification Checklist:\n- Required behavior: checked.\n- Scope safety: changed paths are allowed.\n- Test or command evidence: passed.",
		Edits:     []workspace.PlanEdit{{Purpose: "placeholder without path"}},
		PlanArtifact: &workspace.PlanArtifact{
			Edits: []workspace.PlanEdit{{Path: "internal/example/target.go", OldText: "old", NewText: "new", Purpose: "fix target"}},
		},
	})
	if ok {
		t.Fatal("concrete plan_artifact edits must not be hidden by no-path top-level edits")
	}
}

func TestPlanExecuteTextualAllowedToolsFallsBackToPhaseContract(t *testing.T) {
	allowed := runnerPlanExecuteTextualAllowedTools(nil, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"context_pack", "read_file", "search_content", "code_navigate", "submit_plan"},
	}, []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/example/target.go lines 1-125 (total lines: 391)",
		Output:  "package example\n",
	}})
	if !slices.Contains(allowed, "read_file") {
		t.Fatalf("textual plan-execute surface should preserve phase read tools, got %#v", allowed)
	}
	if slices.Contains(allowed, "submit_plan") {
		t.Fatalf("candidate-source phase must not label invalid submit_plan as allowed, got %#v", allowed)
	}
}

func TestPlanExecuteTextualAllowedToolsExposeSubmitForTypedGroundingCorrection(t *testing.T) {
	allowed := runnerPlanExecuteTextualAllowedTools(nil, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"context_pack", "read_file", "search_content", "code_navigate", "submit_plan"},
	}, nil)
	if !slices.Contains(allowed, "read_file") {
		t.Fatalf("textual plan-execute surface should preserve candidate read tools, got %#v", allowed)
	}
	if slices.Contains(allowed, "submit_plan") {
		t.Fatalf("candidate-source phase must not expose submit_plan until a selected path makes it callable, got %#v", allowed)
	}
}

func TestReviewRepairTextualAllowedToolsKeepsInitialProbeSurface(t *testing.T) {
	allowed := runnerPlanExecuteTextualAllowedTools(nil, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
		Contract:          "review_findings",
		PermissionProfile: "plan_submission_review",
		AllowedTools:      []string{"read_file", "search_content", "submit_plan"},
	}, nil)
	assertContains(t, allowed, "read_file")
	assertContains(t, allowed, "submit_plan")
	assertNotContains(t, allowed, "search_content")
}

func TestReviewRepairNoopSubmitPlanWithChecklistSynthesizesPass(t *testing.T) {
	replies := []string{
		`{"tool":"submit_plan","rationale":"Verification Checklist:\n- Required behavior: checked from phase evidence.\n- Scope safety: changed paths are allowed.\n- Test or command evidence: passed.","edits":[]}`,
		"Decision: pass\n\nVerification Checklist:\n- fallback",
	}
	parsed, _, ok, err := workspace.ParseToolRequest(replies[0])
	if err != nil || !ok || parsed.Tool != "submit_plan" || parsed.Rationale == "" {
		t.Fatalf("test fixture should parse as submit_plan, ok=%t err=%v req=%#v", ok, err, parsed)
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
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: t.TempDir(), CWD: "."}),
		Policy:    RunPolicy{MaxToolSteps: 5, MaxNoProgressTurns: 5},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
			AllowedTools:      []string{"submit_plan"},
			ChangedPaths:      []string{"internal/example/target.go"},
			AllowedEditPaths:  []string{"internal/example/target.go"},
			Objective: strings.Join([]string{
				"Review the applied plan.",
				"",
				"## Phase Evidence Bundle",
				"",
				"```text",
				"phase: review_repair",
				"submitted_plan_state:",
				"- `internal/example/target.go` status=applied summary=edit 1/1: submitted new_text is present error=",
				"source_sanity:",
				"- `internal/example/target.go` status=ok summary=Source sanity passed error=",
				"verification:",
				"- `go test ./...` status=ok summary=ok error=",
				"patch_hygiene: status=passed",
				"```",
			}, "\n"),
		},
	}
	var toolRequests []ToolRequestEvent
	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "review the applied plan"}}, llama.ChatOptions{
		MaxTokens: 2048,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "auto",
	}, nil, func(event ToolRequestEvent) {
		toolRequests = append(toolRequests, event)
	}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if requests != 2 {
		t.Fatalf("expected explicit pass closeout on the second model request, got %d request(s), tool requests: %#v", requests, toolRequests)
	}
	if !strings.Contains(answer, "Decision: pass") || !strings.Contains(answer, "Verification Checklist") {
		t.Fatalf("expected synthesized review-repair pass, got:\n%s", answer)
	}
}

func TestReviewRepairNoopSubmitPlanPassCloseoutRejectsVerificationWithoutOKStatus(t *testing.T) {
	_, ok := reviewRepairNoopSubmitPlanPassCloseout(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
		Contract:          "review_findings",
		PermissionProfile: "plan_submission_review",
		Objective: strings.Join([]string{
			"phase: review_repair",
			"submitted_plan_state:",
			"- `internal/example/target.go` status=applied summary=edit 1/1: submitted new_text is present error=",
			"source_sanity:",
			"- `internal/example/target.go` status=ok summary=Source sanity passed error=",
			"verification:",
			"- `go test ./...` status=pending summary=not yet run error=",
			"patch_hygiene: status=passed",
		}, "\n"),
	}, workspace.ToolRequest{
		Tool:  "submit_plan",
		Edits: []workspace.PlanEdit{},
	})
	if ok {
		t.Fatal("no-op review-repair pass must require verification section status=ok, not any phase status=ok")
	}
}

func TestReviewRepairNoopSubmitPlanPassCloseoutRejectsOpenPhaseObligations(t *testing.T) {
	_, ok := reviewRepairNoopSubmitPlanPassCloseout(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
		Contract:          "review_findings",
		PermissionProfile: "plan_submission_review",
		Objective: strings.Join([]string{
			"phase: review_repair",
			"submitted_plan_state:",
			"- `internal/example/target.go` status=applied summary=edit 1/1: submitted new_text is present error=",
			"source_sanity:",
			"- `internal/example/target.go` status=ok summary=Source sanity passed error=",
			"verification:",
			"- `go test ./...` status=ok summary=ok error=",
			"patch_hygiene: status=passed",
			"open_obligations:",
			"- source_sanity_missing:internal/example/other.go",
		}, "\n"),
	}, workspace.ToolRequest{
		Tool:  "submit_plan",
		Edits: []workspace.PlanEdit{},
	})
	if ok {
		t.Fatal("no-op review-repair pass must not synthesize while Phase Evidence has open obligations")
	}
}

func TestReviewRepairNoopSubmitPlanCloseoutRejectsContradictoryPass(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
		Contract:          "review_findings",
		PermissionProfile: "plan_submission_review",
		Readiness: &workerruntime.ReadinessState{
			OpenObligations: []string{"verification_failed:go test ./..."},
		},
	}
	rejection, rejected := reviewRepairNoopSubmitPlanCloseoutRejection(brief, "Decision: pass\n\nVerification Checklist:\n- Required behavior: checked.\n- Scope safety: checked.\n- Test or command evidence: checked.")
	if !rejected {
		t.Fatal("expected contradictory no-op closeout pass to be rejected")
	}
	answer := reviewRepairNoopSubmitPlanDeterministicCloseout(brief, rejection)
	if !strings.HasPrefix(answer, "<<QUESTION>>:") || !strings.Contains(answer, "verification_failed:go test ./...") {
		t.Fatalf("expected failed obligation blocker, got:\n%s", answer)
	}
	if strings.Contains(answer, "Decision: pass") {
		t.Fatalf("contradictory pass should not be accepted:\n%s", answer)
	}
}

func TestReviewRepairNoopSubmitPlanCloseoutBlocksInvalidTerminalShape(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
		Contract:          "review_findings",
		PermissionProfile: "plan_submission_review",
	}
	rejection, rejected := reviewRepairNoopSubmitPlanCloseoutRejection(brief, "I think this is fine now.")
	if !rejected {
		t.Fatal("expected invalid no-op closeout shape to be rejected")
	}
	answer := reviewRepairNoopSubmitPlanDeterministicCloseout(brief, rejection)
	if !strings.HasPrefix(answer, "<<QUESTION>>:") {
		t.Fatalf("expected deterministic review-repair blocker, got:\n%s", answer)
	}
}

func TestReviewRepairNoopSubmitPlanDeterministicCloseoutPassesWhenPhaseEvidenceAlreadyPassed(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
		Contract:          "review_findings",
		PermissionProfile: "plan_submission_review",
		ChangedPaths:      []string{"internal/labels/labels.go"},
		AllowedEditPaths:  []string{"internal/labels/labels.go"},
		Objective: strings.Join([]string{
			"schema: idiom.phase_evidence.v1",
			"phase: review_repair",
			"changed_paths: internal/labels/labels.go",
			"allowed_repair_paths: internal/labels/labels.go",
			"edit_set_obligations: (none)",
			"",
			"submitted_plan_state:",
			"- `internal/labels/labels.go` status=applied summary=edit 1/1: submitted new_text is present error=",
			"source_sanity:",
			"- `internal/labels/labels.go` status=ok summary=Source sanity passed for 1 file(s) error=",
			"verification:",
			"- `go test ./...` status=ok summary=ok example.com/frozen-label-repo/internal/labels error=",
			"patch_hygiene: status=passed",
		}, "\n"),
	}
	rejection, rejected := reviewRepairNoopSubmitPlanCloseoutRejection(brief, "I think this is fine now.")
	if !rejected {
		t.Fatal("expected invalid terminal shape to be rejected before deterministic closeout")
	}
	answer := reviewRepairNoopSubmitPlanDeterministicCloseout(brief, rejection)
	if !strings.HasPrefix(answer, "Decision: pass\n\nVerification Checklist:") {
		t.Fatalf("expected evidence-backed deterministic pass, got:\n%s", answer)
	}
	if strings.Contains(answer, "<<QUESTION>>:") {
		t.Fatalf("passed phase evidence should not become a blocker:\n%s", answer)
	}
}

func TestReviewRepairNoopSubmitPlanGuardSuppressesAlreadyAppliedPlan(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "widget_config.json"), []byte("{\n  \"labels\": [\"alpha\", \"beta\"]\n}\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
		},
	}
	got := runner.preflightPlanThenExecuteReviewRepairNoopSubmitPlanGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{{
			Path:    "widget_config.json",
			OldText: "\"labels\": [\"alpha\"]",
			NewText: "\"labels\": [\"alpha\", \"beta\"]",
			Purpose: "stale original edit",
		}},
	})
	if !got.Intercepted {
		t.Fatal("review-repair stale submit_plan should be suppressed before submit_plan validation rejects it")
	}
	if got.Result.Tool != "review_repair_noop_plan" || strings.Contains(strings.ToLower(got.Result.Summary), "rejected") {
		t.Fatalf("stale review submit_plan should become a non-error pass nudge, got %#v", got.Result)
	}
}

func TestPlanThenExecuteWriteGuardInactiveOutsidePlannerPhase(t *testing.T) {
	// Same edit_file call that gets rejected during the planner phase
	// must pass through cleanly under any other lane shape.
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{TaskFamily: "implementation"},
	}
	got := runner.preflightPlanThenExecuteWriteGuard(workspace.ToolRequest{Tool: "edit_file", Path: "a.txt"})
	if got.Intercepted {
		t.Fatalf("guard must stand down outside the planner phase, got %#v", got)
	}
}

func TestMutationCompletionGateAcceptsSubmitPlanInPlannerPhase(t *testing.T) {
	// Plan-execute planner phase: submit_plan succeeded but no files
	// were mutated (the executor phase mutates files in a separate
	// process). The completion gate must NOT reject the answer for
	// "no successful write tool result is present" — submit_plan IS
	// the successful write here.
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
	got, rejected := runner.mutationCompletionRejection("Plan submitted with 2 edits.", results)
	if rejected {
		t.Fatalf("planner phase with successful submit_plan must not be rejected by mutation gate; got %#v", got)
	}
}

func TestMutationCompletionGateAcceptsPlannerSourceSatisfiedWithReadEvidence(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.txt lines 1-10 (total lines: 10)"},
		{Tool: "read_file", Summary: "Read f2 b.txt lines 1-10 (total lines: 10)"},
	}
	answer := "Decision: source-satisfied\n- a.txt: already implemented; no changes needed.\n- b.txt: already implemented; no changes needed."
	if got, rejected := runner.mutationCompletionRejection(answer, results); rejected {
		t.Fatalf("source-satisfied planner answer with read evidence should pass, got %#v", got)
	}
	if kind, reason := runner.finalAnswerBlockerKind(answer, results); kind != "" || reason != "" {
		t.Fatalf("source-satisfied planner answer should not be converted to blocker, got kind=%q reason=%q", kind, reason)
	}
}

func TestMutationCompletionGateAcceptsUnscopedPlannerSourceSatisfiedWithReadEvidence(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 pkg/feature.go lines 1-10 (total lines: 10)"},
		{Tool: "read_file", Summary: "Read f2 pkg/feature_test.go lines 1-10 (total lines: 10)"},
	}
	answer := "Decision: source-satisfied\n- pkg/feature.go: requested final state is already present.\n- pkg/feature_test.go: current test coverage already contains the regression."
	if got, rejected := runner.mutationCompletionRejection(answer, results); rejected {
		t.Fatalf("unscoped source-satisfied planner answer with read evidence should pass, got %#v", got)
	}
	if kind, reason := runner.finalAnswerBlockerKind(answer, results); kind != "" || reason != "" {
		t.Fatalf("unscoped source-satisfied planner answer should not be converted to blocker, got kind=%q reason=%q", kind, reason)
	}
}

func TestPlanThenExecuteSourceSatisfiedAnswerCoversPathsRequiresExplicitDecision(t *testing.T) {
	tests := []struct {
		name   string
		answer string
		want   bool
	}{
		{
			name:   "hyphenated explicit decision",
			answer: "Decision: source-satisfied\n- a.txt: already implemented; no changes needed.\n- b.txt: already implemented; no changes needed.",
			want:   true,
		},
		{
			name:   "spaced explicit decision",
			answer: "Decision: source satisfied\n- a.txt: already implemented; no changes needed.\n- b.txt: already implemented; no changes needed.",
			want:   true,
		},
		{
			name:   "already present final state",
			answer: "Decision: source-satisfied\n- a.txt: requested final state is already present.\n- b.txt: current source already contains the requested behavior.",
			want:   true,
		},
		{
			name:   "question mentioning accepted remedy",
			answer: "<<QUESTION>>: Completion gate rejected before submit_plan.\n- a.txt: no successful edit was recorded; provide submit_plan, Decision: source-satisfied evidence, or a path-specific blocker.\n- b.txt: same blocker.",
			want:   false,
		},
		{
			name:   "blocker mentioning accepted remedy",
			answer: "Blocked: a.txt and b.txt need submit_plan or Decision: source-satisfied evidence before closeout.",
			want:   false,
		},
		{
			name:   "punctuated decision is not exact",
			answer: "Decision: source-satisfied:\n- a.txt: already implemented; no changes needed.\n- b.txt: already implemented; no changes needed.",
			want:   false,
		},
		{
			name:   "legacy implicit already-present wording",
			answer: "- a.txt: already implemented; no changes needed.\n- b.txt: already implemented; no changes needed.",
			want:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PlanThenExecuteSourceSatisfiedAnswerCoversPaths(tt.answer, []string{"a.txt", "b.txt"})
			if got != tt.want {
				t.Fatalf("PlanThenExecuteSourceSatisfiedAnswerCoversPaths() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPlanThenExecuteSourceSatisfiedEvidencePathsRequiresExplicitDecision(t *testing.T) {
	answer := "Decision: source-satisfied\n- internal/a.go: no changes needed.\n- internal/a_test.go: no changes needed."
	paths := PlanThenExecuteSourceSatisfiedEvidencePaths(answer)
	want := []string{"internal/a.go", "internal/a_test.go"}
	if !slices.Equal(paths, want) {
		t.Fatalf("evidence paths = %#v, want %#v", paths, want)
	}
	blocker := "<<QUESTION>>: provide Decision: source-satisfied evidence for internal/a.go"
	if paths := PlanThenExecuteSourceSatisfiedEvidencePaths(blocker); len(paths) != 0 {
		t.Fatalf("blocker text must not produce source-satisfied evidence paths: %#v", paths)
	}
}

func TestMutationCompletionGateRejectsPlannerSourceSatisfiedWithoutPathEvidence(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.txt lines 1-10"},
	}
	answer := "Decision: source-satisfied\n- a.txt: already implemented; no changes needed.\n- b.txt: already implemented; no changes needed."
	got, rejected := runner.mutationCompletionRejection(answer, results)
	if !rejected {
		t.Fatalf("source-satisfied answer without evidence for every required path must be rejected")
	}
	if !strings.Contains(got.Error, "source-satisfied") {
		t.Fatalf("rejection should explain the accepted source-satisfied shape, got %#v", got)
	}
}

func TestCompletionGateRecoveryPromptPointsPlanExecuteAtSubmitPlan(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}

	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.txt lines 1-10 (total lines: 10)"},
		{Tool: "read_file", Summary: "Read f2 b.txt lines 1-10 (total lines: 10)"},
	}
	prompt, hideReads, ok := runner.completionGateRecoveryPrompt(results)
	if !ok {
		t.Fatalf("expected completion gate recovery prompt for planner phase with missing required paths")
	}
	if !hideReads {
		t.Fatalf("planner-phase recovery should hide reads and leave submit_plan as the write surface")
	}
	for _, want := range []string{
		"your next response must be a parser-readable `submit_plan` request, not prose",
		"top-level `edits` as a JSON array, not a quoted string",
		"insert_after_line",
		"For small insertions/appends, use `insert_after_line`",
		"Keep edit bodies as small as executable",
		"prefer exact `old_text`/`new_text` for small unique replacements",
		"large additions anchored to unchanged source lines",
		"explicit replacement edit shapes",
		"replace_all:true",
		"empty `old_text`",
		"`new_text` as the full file body",
		"no substantial inline body or explicit replacement shape",
		"one-sentence `purpose`",
		"Every required path already has source-body evidence",
		"read/search/discovery tools are hidden now",
		"Do not call read_file",
		"source-satisfied",
		"a.txt",
		"b.txt",
		"<<QUESTION>>",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("recovery prompt missing %q: %q", want, prompt)
		}
	}
	if strings.Contains(prompt, "edit_file") || strings.Contains(prompt, "apply_patch") {
		t.Fatalf("planner-phase recovery must not point at per-edit tools: %q", prompt)
	}
}

func TestCompletionGateRecoveryPromptPreservesSubmitPlanRejectionDetails(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.txt lines 1-10 (total lines: 10)"},
		{Tool: "read_file", Summary: "Read f2 b.txt lines 1-10 (total lines: 10)"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "plan_artifact.schema_errors edits[0]:invalid_edit_shape:insert_after_line content exceeds inline submit_plan limit (4096 bytes); content_ref is not supported for insert_after_line in this schema",
		},
	}

	prompt, hideReads, ok := runner.completionGateRecoveryPrompt(results)
	if !ok || !hideReads {
		t.Fatalf("expected submit_plan completion recovery with hidden reads, ok=%v hide=%v prompt=%s", ok, hideReads, prompt)
	}
	for _, want := range []string{
		"Previous rejected submit_plan drafts do not count as coverage",
		"Last submit_plan rejection",
		"Next submit_plan coverage checklist",
		"a.txt=must-keep-valid-entry",
		"b.txt=must-keep-valid-entry",
		"Do not repeat `insert_after_line`",
		"`content_ref`",
		"start_line",
		"end_line",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("rejected submit_plan recovery prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestRequiredPathCompletionRecoveryRejectionPreservesSubmitPlanDetails(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.txt lines 1-10 (total lines: 10)"},
		{Tool: "read_file", Summary: "Read f2 b.txt lines 1-10 (total lines: 10)"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "plan_artifact.schema_errors edits[0]:invalid_edit_shape:insert_after_line content exceeds inline submit_plan limit (4096 bytes); content_ref is not supported for insert_after_line in this schema",
		},
	}

	result, rejected := runner.requiredPathCompletionRecoveryRejection("Done.", results)
	if !rejected {
		t.Fatal("expected non-specific completion to be rejected")
	}
	for _, want := range []string{
		"Previous rejected submit_plan drafts do not count as coverage",
		"Last submit_plan rejection",
		"Next submit_plan coverage checklist",
		"Do not repeat `insert_after_line`",
		"start_line",
		"end_line",
	} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("completion rejection missing %q:\n%s", want, result.Error)
		}
	}
}

func TestCompletionGateRecoveryPromptPointsPlanExecuteAtFirstRequiredReadWhenNoEvidence(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}

	prompt, hideReads, ok := runner.completionGateRecoveryPrompt(nil)
	if !ok {
		t.Fatalf("expected completion gate recovery prompt for planner phase with missing required paths")
	}
	if hideReads {
		t.Fatalf("required-path recovery without source evidence must keep source reads visible")
	}
	for _, want := range []string{
		"source-body evidence",
		"Unread required path: a.txt",
		"Do not draft `submit_plan` from outline/search hits",
		"read_file",
		"code_navigate",
		"Hidden reasoning is not executable evidence",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("recovery prompt missing %q: %q", want, prompt)
		}
	}
	for _, notWant := range []string{
		"Every required path already has source-body evidence",
		"read/search/discovery tools are hidden now",
		"Your next response must be a parser-readable `submit_plan` request",
	} {
		if strings.Contains(prompt, notWant) {
			t.Fatalf("recovery prompt should not claim submit_plan readiness before source evidence (%q): %q", notWant, prompt)
		}
	}
}

func TestCompletionGateRecoveryPromptPrioritizesUnreadRequiredSource(t *testing.T) {
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
		{Tool: "code_navigate", Summary: "Code navigation routed file_outline via go_file_outline", Output: "route_summary: Outlined 2 top-level declaration(s) in b.go.\n"},
		{Tool: "search_content", Summary: "Found 2 matching line(s).", Output: "b.go:12:func TestB(t *testing.T)"},
	}

	prompt, hideReads, ok := runner.completionGateRecoveryPrompt(results)
	if !ok {
		t.Fatalf("expected completion gate recovery prompt")
	}
	if hideReads {
		t.Fatalf("unread required source recovery must keep source-body reads visible")
	}
	for _, want := range []string{
		"source-body evidence",
		"Unread required path: b.go",
		"code_navigate",
		"read_symbol",
		"start_line",
		"Do not draft `submit_plan` from outline/search hits",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("recovery prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestDeterministicPlanExecuteRequiredSourceReadStartsFreshPlanner(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}

	req, raw, ok := runner.deterministicPlanExecuteRequiredSourceRead(nil)
	if !ok {
		t.Fatalf("expected deterministic source read for fresh required-path planner")
	}
	if req.Tool != "read_file" || req.Path != "a.txt" {
		t.Fatalf("unexpected recovery request: %#v", req)
	}
	if req.StartLine != 1 || req.EndLine != 180 {
		t.Fatalf("fresh deterministic source read should be bounded to avoid outline-only redirects, got lines %d-%d", req.StartLine, req.EndLine)
	}
	if !strings.Contains(raw, `"tool":"read_file"`) || !strings.Contains(raw, `"path":"a.txt"`) {
		t.Fatalf("raw request should be parser-readable JSON, got %q", raw)
	}

	req, _, ok = runner.deterministicPlanExecuteRequiredSourceRead([]workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.txt lines 1-10"},
	})
	if !ok || req.Tool != "read_file" || req.Path != "a.txt" {
		t.Fatalf("expected deterministic recovery to advance the shallow required-path read, got %#v ok=%v", req, ok)
	}
	if req.StartLine != 11 || req.EndLine != 190 {
		t.Fatalf("advanced deterministic source read should continue the same path, got lines %d-%d", req.StartLine, req.EndLine)
	}

	req, _, ok = runner.deterministicPlanExecuteRequiredSourceRead([]workspace.ToolResult{
		{Tool: "read_file", Summary: "Redirected a.txt to code_navigate", Output: "a.txt requires file_outline"},
	})
	if !ok || req.Tool != "read_file" || req.Path != "b.txt" {
		t.Fatalf("expected deterministic recovery to skip attempted path and advance, got %#v ok=%v", req, ok)
	}

	singlePathRunner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt"},
		},
	}
	req, _, ok = singlePathRunner.deterministicPlanExecuteRequiredSourceRead([]workspace.ToolResult{
		{Tool: "read_file", Summary: "Redirected a.txt to code_navigate", Output: "a.txt requires file_outline"},
	})
	if ok {
		t.Fatalf("deterministic recovery should not repeat a required path already attempted; got %#v", req)
	}
}

func TestDeterministicPlanExecuteRequiredSourceReadFollowsGenericCodeWindow(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"cmd/idiom/product_workflow.go"},
		},
	}

	req, _, ok := runner.deterministicPlanExecuteRequiredSourceRead([]workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read cmd/idiom/product_workflow.go lines 1-180 (file has 2892 lines total)",
			Output:  "1:package main",
		},
	})
	if !ok || req.Tool != "code_navigate" || req.Capability != "file_outline" || req.Path != "cmd/idiom/product_workflow.go" {
		t.Fatalf("generic code header read should deterministically advance to file_outline, got %#v ok=%v", req, ok)
	}
}

func TestPlanExecuteRequiredSourceFollowupRedirectsRepeatedGenericRead(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"cmd/idiom/product_workflow.go"},
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read cmd/idiom/product_workflow.go lines 1-180 (total lines: 2893)",
		Output:  "1:package main",
	}}
	req := workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "cmd/idiom/product_workflow.go",
		StartLine: 1,
		EndLine:   180,
	}

	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: req,
		Raw:     marshalToolRequestRaw(req),
		Results: results,
	})
	if decision.Request.Tool != "code_navigate" ||
		decision.Request.Capability != "file_outline" ||
		decision.Request.Path != "cmd/idiom/product_workflow.go" {
		t.Fatalf("repeated generic required-path read should redirect to source follow-up, got %#v", decision.Request)
	}
}

func TestRepeatedRangeLessReadFileAllowsFullBodyReplay(t *testing.T) {
	runner := &Runner{}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read a.go lines 1-3 (total lines: 3)",
		Output:  "1:package a\n2:\n3:func A() {}\n",
	}}
	summary, errorText, reject := runner.repeatedRangeLessReadFileRejection(workspace.ToolRequest{
		Tool: "read_file",
		Path: "a.go",
	}, results)
	if reject {
		t.Fatalf("repeated range-less read_file after a full read should remain model-owned before broader loop guards, got %q: %s", summary, errorText)
	}
	_, _, reject = runner.repeatedRangeLessReadFileRejection(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "a.go",
		StartLine: 1,
		EndLine:   2,
	}, results)
	if reject {
		t.Fatal("explicit ranged reads must remain available")
	}
}

func TestPlanThenExecuteMalformedSymbolReadRedirectsToBoundedRead(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
		},
	}
	req := workspace.ToolRequest{
		Tool:       "code_navigate",
		Capability: "read_symbol",
		Path:       "internal/productworkflow/coverage_gate.go",
	}
	redirected, raw, ok := runner.planThenExecuteMalformedSymbolReadRedirect(req, nil, []string{"read_file", "code_navigate"})
	if !ok {
		t.Fatal("expected malformed symbol read to redirect")
	}
	if redirected.Tool != "read_file" || redirected.Path != req.Path || redirected.StartLine != 1 || redirected.EndLine != 180 {
		t.Fatalf("redirected request = %#v", redirected)
	}
	if !strings.Contains(raw, `"tool":"read_file"`) || !strings.Contains(raw, `"start_line":1`) {
		t.Fatalf("redirect raw missing bounded read shape: %s", raw)
	}
}

func TestPlanThenExecuteRepeatedSymbolMissRedirectsToModelSelectedBoundedRead(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Zero-hit retry blocked",
		Error:   "this exact code_navigate call already returned no results earlier this turn. Repeating it will not find new matches.",
	}}
	req := workspace.ToolRequest{
		Tool:       "code_navigate",
		Capability: "read_symbol",
		Path:       "internal/productworkflow/coverage_gate.go",
		Symbol:     "CoverageGateResult",
		Pattern:    "CoverageGateResult",
	}

	redirected, raw, ok := runner.planThenExecuteRepeatedSymbolReadFallbackRedirect(req, results, []string{"read_file", "code_navigate"})
	if !ok {
		t.Fatal("expected repeated symbol miss to fall back to bounded source read on the model-selected path")
	}
	if redirected.Tool != "read_file" || redirected.Path != req.Path || redirected.StartLine != 1 || redirected.EndLine != 180 {
		t.Fatalf("redirected request = %#v", redirected)
	}
	if !strings.Contains(raw, `"named_gap":"bounded source window after semantic symbol lookup failed"`) {
		t.Fatalf("redirect raw missing named gap: %s", raw)
	}
}

func TestRepeatedRangeLessReadFileDoesNotMatchPathMentionedInBody(t *testing.T) {
	runner := &Runner{}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read other.go lines 1-3 (total lines: 3)",
		Output:  "1:package other\n2:// TODO: inspect a.go next\n3:func Other() {}\n",
	}}
	_, _, reject := runner.repeatedRangeLessReadFileRejection(workspace.ToolRequest{
		Tool: "read_file",
		Path: "a.go",
	}, results)
	if reject {
		t.Fatal("body text mentioning a path must not count as typed evidence that the requested path was already fully read")
	}
}

func TestRepeatedRangeLessReadFileAfterOutlineReturnsSourceBodyShape(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Objective: "update product workflow",
		},
	}
	results := []workspace.ToolResult{
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
				"route_summary: Outlined 2 top-level declaration(s) in cmd/idiom/product_workflow.go.",
				"declarations:",
				"- buildAuditResult lines 313-354 tags: core",
				"- runProductWorkflow lines 1396-1671 tags: implementation",
			}, "\n"),
		},
	}
	_, errorText, reject := runner.repeatedRangeLessReadFileRejection(workspace.ToolRequest{
		Tool: "read_file",
		Path: "cmd/idiom/product_workflow.go",
	}, results)
	if !reject {
		t.Fatal("expected repeated range-less read after outline redirect to be rejected")
	}
	for _, want := range []string{
		"already redirected to outline evidence",
		`"tool":"code_navigate"`,
		`"capability":"read_symbol"`,
		`"pattern":"runProductWorkflow"`,
	} {
		if !strings.Contains(errorText, want) {
			t.Fatalf("outline-repeat rejection missing %q:\n%s", want, errorText)
		}
	}
	if strings.Contains(errorText, `"tool":"read_file","path":"cmd/idiom/product_workflow.go"`) &&
		!strings.Contains(errorText, `"start_line"`) {
		t.Fatalf("outline-repeat rejection must not return a range-less full-file read shape:\n%s", errorText)
	}
}

func TestRepeatedRangeLessReadFileAfterOutlineRedirectsToSourceBodyShape(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Objective: "update product workflow",
		},
	}
	results := []workspace.ToolResult{
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
				"route_summary: Outlined 2 top-level declaration(s) in cmd/idiom/product_workflow.go.",
				"declarations:",
				"- buildAuditResult lines 313-354 tags: core",
				"- runProductWorkflow lines 1396-1671 tags: implementation",
			}, "\n"),
		},
	}
	redirected, raw, ok := runner.repeatedRangeLessReadFileRedirect(workspace.ToolRequest{
		Tool: "read_file",
		Path: "cmd/idiom/product_workflow.go",
	}, results, []string{"read_file", "code_navigate"})
	if !ok {
		t.Fatal("expected repeated range-less read after outline redirect to be rewritten")
	}
	if redirected.Tool != "code_navigate" || redirected.Capability != "read_symbol" || redirected.Pattern != "runProductWorkflow" {
		t.Fatalf("unexpected redirected request: %#v", redirected)
	}
	if !strings.Contains(raw, `"tool":"code_navigate"`) || !strings.Contains(raw, `"capability":"read_symbol"`) {
		t.Fatalf("redirect raw should be parser-readable bounded source-body request, got %s", raw)
	}
}

func TestRepeatedRangeLessReadFileRedirectDoesNotRewriteAfterConcreteSource(t *testing.T) {
	runner := &Runner{}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read cmd/idiom/product_workflow.go lines 1-3 (total lines: 3)",
			Output:  "1:package main\n2:\n3:func runProductWorkflow() {}\n",
		},
	}
	_, _, ok := runner.repeatedRangeLessReadFileRedirect(workspace.ToolRequest{
		Tool: "read_file",
		Path: "cmd/idiom/product_workflow.go",
	}, results, []string{"read_file", "code_navigate"})
	if ok {
		t.Fatal("full source evidence should keep duplicate range-less reads on the rejection path")
	}
}

func TestPlanExecuteRequiredSourceFollowupRedirectAllowsAdvancingRange(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"internal/productworkflow/coverage_gate.go"},
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read internal/productworkflow/coverage_gate.go lines 1-180 (total lines: 338)",
		Output:  "1:package productworkflow",
	}}
	req := workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "internal/productworkflow/coverage_gate.go",
		StartLine: 181,
		EndLine:   360,
	}

	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: req,
		Raw:     marshalToolRequestRaw(req),
		Results: results,
	})
	if decision.Request.Tool != req.Tool || decision.Request.Path != req.Path || decision.Request.StartLine != req.StartLine || decision.Request.EndLine != req.EndLine {
		t.Fatalf("bounded follow-up range should be allowed as advancing evidence, got %#v", decision.Request)
	}
}

func TestPlanExecuteRequiredSourceFollowupRedirectsLargeGoRangeToOutline(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"cmd/idiom/product_workflow_test.go"},
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read cmd/idiom/product_workflow_test.go lines 1-300 (total lines: 6870)",
		Output:  "1:package main",
	}}
	req := workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "cmd/idiom/product_workflow_test.go",
		StartLine: 301,
		EndLine:   600,
	}

	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: req,
		Raw:     marshalToolRequestRaw(req),
		Results: results,
	})
	if decision.Request.Tool != "code_navigate" ||
		decision.Request.Path != "cmd/idiom/product_workflow_test.go" ||
		decision.Request.Capability != "file_outline" {
		t.Fatalf("large Go range follow-up should redirect to outline before broad scanning, got %#v", decision.Request)
	}
}

func TestPlanExecuteRequiredSourceFollowupRedirectsGenericRangeAfterOutline(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			Objective:         "Implement product TDD coverage gate behavior",
			RequiredEditPaths: []string{"cmd/idiom/product_workflow.go"},
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed file_outline via go_file_outline",
		Output: strings.Join([]string{
			"capability: file_outline",
			"route_summary: Outlined 2 top-level declaration(s) in cmd/idiom/product_workflow.go.",
			"declarations:",
			"- buildAuditResult lines 313-354 tags: core",
			"- runProductTDD lines 1396-1671 tags: implementation",
		}, "\n"),
	}}
	req := workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "cmd/idiom/product_workflow.go",
		StartLine: 1,
		EndLine:   100,
	}

	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: req,
		Raw:     marshalToolRequestRaw(req),
		Results: results,
	})
	if decision.Request.Tool != "code_navigate" ||
		decision.Request.Path != "cmd/idiom/product_workflow.go" ||
		decision.Request.Capability != "read_symbol" ||
		decision.Request.Pattern != "runProductTDD" {
		t.Fatalf("generic range after outline should redirect to symbol source, got %#v", decision.Request)
	}
}

func TestPlanExecuteRequiredSourceFollowupRedirectsSatisfiedPathDriftToPendingPath(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			Objective:         "Implement product TDD coverage gate behavior",
			RequiredEditPaths: []string{"internal/productworkflow/coverage_gate.go", "cmd/idiom/product_workflow.go"},
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read ValidateCoverage in internal/productworkflow/coverage_gate.go lines 20-80\n\n20:func ValidateCoverage() {}\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read cmd/idiom/product_workflow.go lines 1-180 (total lines: 2893)",
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
	}
	req := workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "internal/productworkflow/coverage_gate.go",
		StartLine: 1,
		EndLine:   300,
	}

	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: req,
		Raw:     marshalToolRequestRaw(req),
		Results: results,
	})
	if decision.Request.Tool != "code_navigate" ||
		decision.Request.Path != "cmd/idiom/product_workflow.go" ||
		decision.Request.Capability != "read_symbol" ||
		decision.Request.Pattern != "runProductTDD" {
		t.Fatalf("drift to already-satisfied required path should redirect to pending path follow-up, got %#v", decision.Request)
	}
}

func TestPlanExecuteRequiredSourceFollowupRedirectPreservesInvalidAnchorRefresh(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/productworkflow/coverage_gate.go"},
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n\n1515:func runProductTDD() {}\n",
		},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "invalid edit anchor(s): `cmd/idiom/product_workflow.go: old_text not found in current source`; missing required edit path(s): `internal/productworkflow/coverage_gate.go`.",
		},
	}
	req := workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "cmd/idiom/product_workflow.go",
		StartLine: 1515,
		EndLine:   1640,
	}

	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: req,
		Raw:     marshalToolRequestRaw(req),
		Results: results,
	})
	if decision.Request.Tool != req.Tool || decision.Request.Path != req.Path || decision.Request.StartLine != req.StartLine || decision.Request.EndLine != req.EndLine {
		t.Fatalf("bounded invalid-anchor refresh should not be redirected, got %#v", decision.Request)
	}
}

func TestPlanExecuteReadySourceRefreshRequiresRejectedPlanAnchorGap(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/productworkflow/coverage_gate.go"},
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
			Summary: "Read internal/productworkflow/coverage_gate.go lines 1-300 (total lines: 338)",
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
		Error:   "submit_plan must include at least one edit entry for every required_edit_paths entry; invalid edit anchor(s): `cmd/idiom/product_workflow.go: old_text not found in current source`.",
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
			Summary: "Read f10 internal/app/rendering.go lines 1-180",
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
		"Read candidate path(s): internal/app/rendering.go",
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
			TargetRefs:        []string{"internal/app/rendering.go", "docs/banner.md"},
		},
	}

	req, _, ok := runner.deterministicNoWriteRecoveryTargetRead(nil)
	if !ok {
		t.Fatalf("expected deterministic target read for candidate-scope planner")
	}
	if req.Tool != "read_file" || req.Path != "internal/app/rendering.go" || req.StartLine != 1 || req.EndLine != 180 {
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
	}, []workspace.ToolResult{{Tool: "read_file", Summary: "Read f1 internal/app/rendering.go lines 1-120"}})

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
		Objective:         "Create internal/writerroute/decision.go",
		RequiredEditPaths: []string{"internal/writerroute/decision.go"},
	}, []workspace.ToolResult{{Tool: "read_file", Summary: "Read f1 internal/writerroute/decision.go", Error: "path is a NEW FILE and does not exist yet"}})

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
		RequiredEditPaths: []string{"internal/app/app.go", "internal/agent/runner.go"},
	}, []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 internal/app/app.go lines 1-56 (total lines: 56)"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read Runner.Run in internal/agent/runner.go lines 88-210"},
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
		RequiredEditPaths: []string{"internal/app/app.go", "internal/agent/runner.go"},
	}, []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 internal/app/app.go lines 1-56 (total lines: 56)"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read Runner.Run in internal/agent/runner.go lines 88-210"},
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
	for _, want := range []string{"source-ready", "submit_plan", "one edit per required path", "Do not call `read_file`"} {
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

func TestPlanExecuteCandidateReadyTerminalPromptIncludesSubmitSkeleton(t *testing.T) {
	runner := &Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedEditPaths:  []string{"internal/productworkflow/coverage_gate.go"},
	}}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/productworkflow/coverage_gate.go lines 1-391 (total lines: 391)",
		Output:  "1:package productworkflow\n",
	}}

	prompt := runner.planThenExecuteSubmitReadyTerminalPrompt("Candidate source already ready", results, false)
	for _, want := range []string{
		"Do not read, search, inspect, or verify again",
		"# Current Submit Plan Skeleton",
		"top-level `tool:\"submit_plan\"`",
		"internal/productworkflow/coverage_gate.go",
		"Do not copy placeholders",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("terminal candidate prompt missing %q:\n%s", want, prompt)
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

func TestPlanExecuteTextualAllowedToolsKeepsHistoryWhenCandidateReadyNeedsHistory(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:       PlanThenExecutePlannerTaskFamily,
		AllowedEditPaths: []string{"pkg/feature.go"},
		RequiredEvidence: []string{requiredEvidenceGitHistoryRationale},
		AllowedTools:     []string{"history_range", "history_symbol", "submit_plan"},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 pkg/feature.go lines 1-10 (total lines: 10)",
			Output:  "1:package pkg\n",
		},
		{
			Tool:    "read_file",
			Code:    planThenExecuteResultCandidateSourceReady,
			Summary: "arbitrary candidate terminal nudge",
			Error:   "candidate/allowed scope already has anchor-ready source evidence. Submit one parser-readable submit_plan.",
		},
	}

	allowed := runnerPlanExecuteTextualAllowedTools([]string{"history_range", "history_symbol", "submit_plan"}, brief, results)
	for _, want := range []string{"history_range", "history_symbol", "submit_plan"} {
		if !slices.Contains(allowed, want) {
			t.Fatalf("candidate-ready history-pending surface missing %s: %#v", want, allowed)
		}
	}

	narrowed := runnerPlanExecuteTextualAllowedTools([]string{"submit_plan"}, brief, results)
	for _, want := range []string{"history_range", "history_symbol", "submit_plan"} {
		if !slices.Contains(narrowed, want) {
			t.Fatalf("candidate-ready history-pending surface should restore %s after submit-only narrowing: %#v", want, narrowed)
		}
	}
}

func TestPlanExecuteTextualAllowedToolsKeepsReadToolsWhenHistoryPending(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"pkg/feature.go"},
		RequiredEvidence:  []string{requiredEvidenceGitHistoryRationale},
		AllowedTools:      []string{"read_file", "code_navigate", "history_range", "history_symbol", "submit_plan"},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 pkg/feature.go lines 1-10 (total lines: 10)",
			Output:  "1:package pkg\n",
		},
		{
			Tool:    "read_file",
			Code:    planThenExecuteResultCandidateSourceReady,
			Summary: "required path has source evidence",
			Error:   "candidate/allowed scope already has anchor-ready source evidence. Submit one parser-readable submit_plan.",
		},
	}

	allowed := runnerPlanExecuteTextualAllowedTools([]string{"history_range", "history_symbol", "submit_plan"}, brief, results)
	for _, want := range []string{"read_file", "code_navigate", "history_range", "history_symbol", "submit_plan"} {
		if !slices.Contains(allowed, want) {
			t.Fatalf("history-pending surface should preserve %s for exact missing edit-safety evidence: %#v", want, allowed)
		}
	}
}

func TestPlanExecuteCandidateReadyResultUsesTypedCodeBeforeSummary(t *testing.T) {
	result := workspace.ToolResult{
		Code:    planThenExecuteResultCandidateSourceReady,
		Summary: "not the legacy summary",
		Error:   "candidate scope is ready",
	}
	if !planThenExecuteCandidateSourceReadyResult(result) {
		t.Fatal("typed candidate-source-ready code should be authoritative even with arbitrary summary text")
	}
	if !planThenExecuteCandidateSourceReadyRejectionSeen([]workspace.ToolResult{result}) {
		t.Fatal("typed candidate-source-ready code should drive candidate-ready rejection detection")
	}
}

func TestPlanExecuteCandidateTerminalSubmitPlanWaitsForUnreadAllowedCandidate(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:       PlanThenExecutePlannerTaskFamily,
		AllowedEditPaths: []string{"pkg/feature.go", "pkg/feature_test.go"},
	}
	if planThenExecuteTerminalSubmitPlanAllowed(brief, []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 pkg/feature.go lines 1-4"},
	}) {
		t.Fatal("candidate scope with unread allowed candidates should not collapse textual surface to submit_plan-only")
	}
	if !planThenExecuteTerminalSubmitPlanAllowed(brief, []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 pkg/feature.go lines 1-4"},
		{Tool: "read_file", Summary: "Read f2 pkg/feature_test.go lines 1-4"},
	}) {
		t.Fatal("candidate scope should become terminal once every allowed candidate has source evidence")
	}
}

func TestPlanExecuteOpenCandidateTerminalDoesNotCollapseAfterOneRead(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 internal/example/target.go lines 1-80"},
	}
	if planThenExecuteTerminalSubmitPlanAllowed(brief, results) {
		t.Fatal("unscoped candidate source without allowed paths should not collapse to submit_plan-only after one read")
	}
	if !planThenExecuteOpenCandidateSourceCanSubmit(brief, results) {
		t.Fatal("unscoped candidate source with source evidence should allow submit_plan while keeping read/search available")
	}
}

func TestPlanExecuteOpenCandidateTerminalWaitsForUnreadContextCandidate(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "context pack",
			Output:  `{"candidate_files":[{"path":"pkg/feature.go"},{"path":"pkg/feature_test.go"}]}`,
		},
		{
			Tool:    "read_file",
			Summary: "Read f1 pkg/feature.go lines 1-80 (total lines: 80)",
			Output:  "1:package pkg\n",
		},
	}
	if planThenExecuteTerminalSubmitPlanAllowed(brief, results) {
		t.Fatal("unscoped candidate scope with unread context candidates should not collapse to submit-plan-only")
	}
	if !planThenExecuteOpenCandidateSourceCanSubmit(brief, results) {
		t.Fatal("unscoped candidate source with source evidence should still allow submit_plan while reads remain visible")
	}
}

func TestPlanExecuteCompletionRecoveryKeepsReadsForUnreadContextCandidate(t *testing.T) {
	thinkingOn := true
	opts := completionGateRecoveryChatOptions(llama.ChatOptions{
		EnableThinking:   &thinkingOn,
		PreserveThinking: true,
		MaxTokens:        16384,
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
	}, []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "context pack",
			Output:  `{"candidate_files":[{"path":"pkg/feature.go"},{"path":"pkg/feature_test.go"}]}`,
		},
		{
			Tool:    "read_file",
			Summary: "Read f1 pkg/feature.go lines 1-80 (total lines: 80)",
			Output:  "1:package pkg\n",
		},
	})

	if opts.ToolChoice == "required" {
		t.Fatalf("completion recovery with unread context candidates must not force terminal submit_plan mode")
	}
	if !chatOptionsExposeTool(opts, "read_file") || !chatOptionsExposeTool(opts, "search_content") || !chatOptionsExposeTool(opts, "submit_plan") {
		t.Fatalf("completion recovery should keep read/search and submit_plan available, got %#v", opts.Tools)
	}
}

func TestPlanExecuteUnreadCandidateRecoveryDropsMissingFileEvidence(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "context pack",
			Output:  `{"candidate_files":[{"path":"cmd/idiom/analysis_agents.go"},{"path":"internal/productworkflow/coverage_gate.go"}]}`,
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/productworkflow/coverage_gate.go lines 1-80 (total lines: 391)",
			Output:  "1:package productworkflow\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read cmd/idiom/analysis_agents.go failed",
			Error:   "file not found: cmd/idiom/analysis_agents.go",
		},
	}

	unread := unreadPlanThenExecuteCandidateScopePathsForRecovery(brief, results, 8)
	if slices.Contains(unread, "cmd/idiom/analysis_agents.go") {
		t.Fatalf("missing candidate path should not stay in unread recovery list: %#v", unread)
	}
}

func TestPlanExecuteLoopBudgetRecoveryAfterSymbolMissKeepsUnreadCandidatePath(t *testing.T) {
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
			Summary: "Context pack wrote .idiom/comprehension/views/test.json with 2 candidate file(s)",
			Output:  `{"candidate_files":[{"path":"internal/productworkflow/coverage_gate.go"},{"path":"internal/productcmd/product_workflow.go"}]}`,
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "capability: read_symbol\nlanguage: go\nrouted_tool: go_read_symbol\nroute_summary: No Go symbols matched \"CoverageGate\"",
		},
	}

	recovery, ok := runner.planThenExecuteLoopBudgetRecoveryPrompt(
		workspace.ToolRequest{Tool: "code_navigate", Path: "internal/productworkflow/coverage_gate.go", Capability: "read_symbol", Symbol: "CoverageGate", Pattern: "CoverageGate"},
		results,
		"the model repeated code_navigate with the same arguments 3 turns in a row",
		false,
	)
	if !ok {
		t.Fatal("expected loop recovery to stay model-owned on unread candidate paths after a symbol miss")
	}
	if recovery.TerminalOnly {
		t.Fatalf("symbol miss without source body should not force terminal-only recovery:\n%s", recovery.Prompt)
	}
	for _, want := range []string{
		"Unread candidate path(s):",
		"internal/productcmd/product_workflow.go",
		"internal/productworkflow/coverage_gate.go",
		"one bounded source-body read/search for a model-selected unread candidate path",
		"a bounded read on that same candidate path is valid",
		"Do not call the same tool with the same arguments again",
	} {
		if !strings.Contains(recovery.Prompt, want) {
			t.Fatalf("recovery prompt missing %q:\n%s", want, recovery.Prompt)
		}
	}
}

func TestPlanExecuteLoopBudgetRecoveryDoesNotUseUnreadCandidateEscapeAfterSourceBody(t *testing.T) {
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
			Summary: "Context pack",
			Output:  `{"candidate_files":[{"path":"internal/productworkflow/coverage_gate.go"},{"path":"internal/productcmd/product_workflow.go"}]}`,
		},
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/productworkflow/coverage_gate.go lines 1-80 (total lines: 320)",
			Output:  "1:package productworkflow\n2:func EvaluateCoverageGate() {}\n",
		},
	}

	recovery, ok := runner.planThenExecuteLoopBudgetRecoveryPrompt(
		workspace.ToolRequest{Tool: "read_file", Path: "internal/productworkflow/coverage_gate.go"},
		results,
		"the model repeated read_file with the same arguments 3 turns in a row",
		false,
	)
	if !ok {
		t.Fatal("source-body evidence should still get terminal loop recovery")
	}
	if !recovery.TerminalOnly {
		t.Fatalf("source-body evidence plus unread candidates should not become a generic read escape:\n%s", recovery.Prompt)
	}
}

func TestPlanExecuteUnscopedEmptySubmitPlanRejectionReopensCandidateSurface(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote .idiom/comprehension/views/test.json with 2 candidate file(s)",
			Output:  `{"candidate_files":[{"path":"pkg/feature.go"},{"path":"pkg/feature_test.go"}]}`,
		},
		{
			Tool:    "read_file",
			Summary: "Read f1 pkg/feature.go lines 1-80 (total lines: 80)",
			Output:  "1:package pkg\n2:\n3:func Label() string { return \"long label\" }\n",
		},
		{
			Tool:    "read_file",
			Code:    planThenExecuteResultCandidateSourceReady,
			Summary: "Candidate source already ready",
			Error:   "candidate source evidence is ready for a terminal planner outcome",
		},
		{
			Tool:    "submit_plan",
			Code:    workspace.SubmitPlanEmptyEditsCode,
			Summary: "submit_plan rejected",
			Error:   "submit_plan requires at least one edit in the edits array",
		},
	}
	if !planThenExecuteUnscopedEmptySubmitPlanRejected(brief, results) {
		t.Fatal("empty submit_plan rejection should be detected from the typed result code")
	}
	if planThenExecuteSubmitPlanOnlyReady(brief, results) {
		t.Fatal("unscoped empty submit_plan rejection must not keep the planner in submit-plan-only mode")
	}
	if planThenExecuteTerminalSubmitPlanAllowed(brief, results) {
		t.Fatal("unscoped empty submit_plan rejection must reopen candidate source/search surface")
	}
	if planThenExecuteRejectedPlanCorrectionOnly(brief, results) {
		t.Fatal("unscoped empty submit_plan rejection is salvage, not submit-plan-only correction")
	}
	allowed := runnerPlanExecuteTextualAllowedTools([]string{"context_pack", "read_file", "code_navigate", "submit_plan"}, brief, results)
	if slices.Equal(allowed, []string{"submit_plan"}) {
		t.Fatalf("textual allowed tools collapsed to submit_plan after empty rejection: %#v", allowed)
	}
	if !slices.Contains(allowed, "read_file") || !slices.Contains(allowed, "submit_plan") {
		t.Fatalf("candidate recovery should keep both source evidence and non-empty submit_plan available, got %#v", allowed)
	}
}

func TestPlanExecuteUnscopedEmptySubmitPlanRejectionRequiresTypedCode(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}
	results := []workspace.ToolResult{{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected",
		Error:   "submit_plan requires at least one edit in the edits array",
	}}
	if planThenExecuteUnscopedEmptySubmitPlanRejected(brief, results) {
		t.Fatal("empty submit_plan salvage must be driven by typed result code, not diagnostic prose")
	}
}

func TestPlanExecuteRunnerReopensCandidateSurfaceAfterEmptySubmitPlan(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n\nfunc Label() string { return \"Long descriptive label\" }\n"), 0o644); err != nil {
		t.Fatalf("write source fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "a_test.go"), []byte("package a\n\nfunc TestLabel(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatalf("write test fixture: %v", err)
	}
	replies := []string{
		`{"tool":"context_pack","task":"shorten the label returned by a.go and keep tests aligned","mode":"plan","limit":4}`,
		`{"tool":"read_file","path":"a.go","start_line":1,"end_line":20}`,
		`{"tool":"submit_plan","rationale":"drafting from current candidate evidence","edits":[]}`,
		`{"tool":"read_file","path":"a_test.go","start_line":1,"end_line":20,"named_gap":"check adjacent test expectation before finalizing the edit"}`,
		`{"tool":"submit_plan","rationale":"Shorten the label using current source evidence.","edits":[{"path":"a.go","old_text":"return \"Long descriptive label\"","new_text":"return \"Short label\"","purpose":"shorten the user-visible label"}]}`,
	}
	var requests int
	var chatRequests []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode chat request: %v", err)
		}
		chatRequests = append(chatRequests, payload)
		reply := replies[requests]
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))
	defer server.Close()

	var sawEmptyRecovery bool
	var sawEmptyRejection bool
	var sawPostEmptyRead bool
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "plan_then_execute_empty_submit_plan_recovery" {
				sawEmptyRecovery = true
			}
		},
	}
	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "shorten the label and keep tests aligned"}},
		llama.ChatOptions{MaxTokens: 2048},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Request.Tool == "submit_plan" && event.Result.Code == workspace.SubmitPlanEmptyEditsCode {
				sawEmptyRejection = true
			}
			if sawEmptyRejection && event.Request.Tool == "read_file" && event.Request.Path == "a_test.go" && strings.TrimSpace(event.Result.Error) == "" {
				sawPostEmptyRead = true
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if requests != len(replies) {
		t.Fatalf("expected full empty-plan recovery sequence, got %d request(s)", requests)
	}
	if !sawEmptyRecovery {
		t.Fatal("runner did not emit empty submit_plan recovery event")
	}
	if !sawPostEmptyRead {
		t.Fatal("runner did not execute a bounded candidate read after empty submit_plan rejection")
	}
	if len(chatRequests) < 4 {
		t.Fatalf("expected at least four model calls, got %d", len(chatRequests))
	}
	afterEmptyRecovery := chatRequests[3]
	if choice, ok := afterEmptyRecovery["tool_choice"].(string); ok && choice == "required" {
		t.Fatalf("empty-plan recovery must not preserve terminal required tool_choice: %#v", afterEmptyRecovery["tool_choice"])
	}
	if responseFormat, ok := afterEmptyRecovery["response_format"].(map[string]any); ok && responseFormat["type"] == "json_object" {
		t.Fatalf("empty-plan recovery must not preserve terminal JSON response_format: %#v", responseFormat)
	}
	if !chatRequestToolListContains(afterEmptyRecovery, "read_file") || !chatRequestToolListContains(afterEmptyRecovery, "submit_plan") {
		t.Fatalf("empty-plan recovery request should expose candidate tools plus submit_plan, got tools=%#v", afterEmptyRecovery["tools"])
	}
	if chatRequestMessagesContain(afterEmptyRecovery, "# Terminal Plan-Execute Native Tool Surface") ||
		chatRequestMessagesContain(afterEmptyRecovery, "# Terminal Plan-Execute Textual Tool Surface") {
		t.Fatalf("empty-plan recovery request retained stale terminal surface scaffolding")
	}
	if !strings.Contains(answer, "Plan submitted") {
		t.Fatalf("expected final non-empty submit_plan to be accepted, got:\n%s", answer)
	}
}

func chatRequestToolListContains(payload map[string]any, name string) bool {
	tools, ok := payload["tools"].([]any)
	if !ok {
		return false
	}
	for _, rawTool := range tools {
		tool, ok := rawTool.(map[string]any)
		if !ok {
			continue
		}
		function, ok := tool["function"].(map[string]any)
		if !ok {
			continue
		}
		if function["name"] == name {
			return true
		}
	}
	return false
}

func chatRequestMessagesContain(payload map[string]any, needle string) bool {
	messages, ok := payload["messages"].([]any)
	if !ok {
		return false
	}
	for _, rawMessage := range messages {
		message, ok := rawMessage.(map[string]any)
		if !ok {
			continue
		}
		if strings.Contains(fmt.Sprint(message["content"]), needle) {
			return true
		}
	}
	return false
}

func TestPlanExecuteSubmitReadyFailureFeedbackDoesNotSuggestGenericProbe(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.go", "a_test.go"},
	}
	req := workspace.ToolRequest{Tool: "read_file", Path: "a.go", StartLine: 1, EndLine: 20}
	rejected := workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Plan-execute submit_plan required",
		Error:   "read_file is not available because the plan-execute planner is source-ready and exposes only submit_plan",
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.go lines 1-20 (total lines: 20)"},
		{Tool: "read_file", Summary: "Read f2 a_test.go lines 1-20 (total lines: 20)"},
		rejected,
	}

	state := buildRunnerNextActionState("main", 3, "tool_rejected", req, rejected, results, false, brief)
	joinedState := strings.Join(state.NextValidActions, "\n")
	if !strings.Contains(joinedState, "submit_plan with one ordered edit entry") {
		t.Fatalf("submit-ready next_action_state should point at submit_plan, got %#v", state.NextValidActions)
	}
	if strings.Contains(joinedState, "one exact tool-shaped follow-up") {
		t.Fatalf("submit-ready next_action_state must not suggest another probe: %#v", state.NextValidActions)
	}
	if strings.Contains(joinedState, "answer from the current evidence") {
		t.Fatalf("submit-ready next_action_state must not include generic answer/probe fallback: %#v", state.NextValidActions)
	}
	packet := buildRunnerFailureFeedbackPacket("main", 3, "tool_rejected", req, rejected, results, false, brief)
	joinedFeedback := strings.Join(packet.ValidNext, "\n")
	if !strings.Contains(joinedFeedback, "submit_plan with one ordered edit entry") {
		t.Fatalf("submit-ready failure feedback should point at submit_plan, got %#v", packet.ValidNext)
	}
	if strings.Contains(joinedFeedback, "one exact tool-shaped follow-up") {
		t.Fatalf("submit-ready failure feedback must not suggest another probe: %#v", packet.ValidNext)
	}
	if strings.Contains(joinedFeedback, "answer from the current evidence") {
		t.Fatalf("submit-ready failure feedback must not include generic answer/probe fallback: %#v", packet.ValidNext)
	}
	if strings.Contains(packet.ResultError, "Use an available tool") {
		t.Fatalf("submit-ready rejection should not use generic allowed-tool wording: %q", packet.ResultError)
	}
}

func TestPlanExecuteSubmitReadyStateDoesNotAdvertiseReadProbes(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.go", "b.go"},
		RequiredNext: []string{
			"for existing required paths, satisfy source-body obligations before submit_plan unless the packet is exact-plan-ready/source-satisfied; then call submit_plan, answer Decision: source-satisfied with per-path evidence, or report <<QUESTION>>",
		},
		SearchPolicy: []string{
			"treat required_edit_paths plus packet evidence as submit_plan scope; packet anchors establish scope but existing paths still need source-body obligations satisfied before submit_plan",
			"use reads only for exact anchor confirmation before submit_plan",
		},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.go lines 1-20 (total lines: 20)"},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read Run in b.go lines 10-30\nfunc Run() {}\n",
		},
	}

	readyBrief := runnerBriefWithRequiredEditPathHints(brief, results)
	joinedNext := strings.Join(readyBrief.RequiredNext, "\n")
	joinedPolicy := strings.Join(readyBrief.SearchPolicy, "\n")
	if !strings.Contains(joinedNext, "submit_plan with one ordered edit entry") {
		t.Fatalf("submit-ready brief should keep terminal submit_plan action, got %#v", readyBrief.RequiredNext)
	}
	if !strings.Contains(joinedPolicy, "no read/search/discovery action is valid after submit_plan readiness") {
		t.Fatalf("submit-ready brief should add terminal no-read policy, got %#v", readyBrief.SearchPolicy)
	}
	for _, forbidden := range []string{
		"source-body obligations before submit_plan",
		"use reads only for exact anchor confirmation",
		"raw source reads are only for exact anchor confirmation",
	} {
		if strings.Contains(joinedNext+"\n"+joinedPolicy, forbidden) {
			t.Fatalf("submit-ready brief leaked stale source-read phrase %q\nnext=%#v\npolicy=%#v", forbidden, readyBrief.RequiredNext, readyBrief.SearchPolicy)
		}
	}

	lastReq := workspace.ToolRequest{Tool: "code_navigate", Path: "b.go", Capability: "read_symbol", Pattern: "Run"}
	lastResult := results[len(results)-1]
	state := buildRunnerNextActionState("main", 2, "tool_result", lastReq, lastResult, results, false, brief)
	joinedState := strings.Join(state.NextValidActions, "\n")
	if !strings.Contains(joinedState, "submit_plan with one ordered edit entry") {
		t.Fatalf("submit-ready next_action_state should point at submit_plan, got %#v", state.NextValidActions)
	}
	for _, forbidden := range []string{
		"one exact missing claim",
		"narrow range read",
		"exact-text search",
		"answer if the inspected body",
		"source-body obligations before submit_plan",
	} {
		if strings.Contains(joinedState, forbidden) {
			t.Fatalf("submit-ready next_action_state leaked read/probe action %q: %#v", forbidden, state.NextValidActions)
		}
	}
}

func TestPlanExecuteSubmitReadyParseRecoveryBlocksFurtherReads(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.go", "a_test.go"},
	}
	runner := &Runner{RuntimeBrief: brief}
	sourceReady := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.go lines 1-20 (total lines: 20)"},
		{Tool: "read_file", Summary: "Read f2 a_test.go lines 1-20 (total lines: 20)"},
	}
	raw := `{"tool":"submit_plan","rationale":"Update coverage gate","edits":[{"path":"a.go""start_line":1}]}`
	prompt, ok := runner.planThenExecuteSubmitPlanParseRecoveryPrompt(raw, errors.New(`invalid character '"' after object key:value pair`), sourceReady)
	if !ok {
		t.Fatalf("expected submit-ready malformed JSON to trigger submit_plan parse recovery")
	}
	if !strings.Contains(prompt, "malformed") || !strings.Contains(prompt, "Do not call read/search/discovery") {
		t.Fatalf("expected terminal parse recovery prompt, got %q", prompt)
	}

	afterParse := append(append([]workspace.ToolResult{}, sourceReady...), workspace.ToolResult{
		Tool:    "tool_parse",
		Summary: "Malformed tool request rejected",
		Error:   "decode tool request",
		Output:  raw,
	})
	if !planThenExecuteSubmitReadyParseFailureSeen(brief, afterParse) {
		t.Fatal("expected submit-ready parse failure to be remembered")
	}
	if runner.planThenExecuteReadySourceRefreshAllowed(workspace.ToolRequest{Tool: "read_file", Path: "a.go", StartLine: 1, EndLine: 20}, afterParse, 0) {
		t.Fatal("post-readiness source refresh must not reopen after malformed submit_plan JSON")
	}
	afterRejectedPlan := append(append([]workspace.ToolResult{}, afterParse...), workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected: required edit path coverage mismatch",
		Error:   "invalid edit anchor(s): `a.go: old_text not found in current source`.",
	})
	if !runner.planThenExecuteReadySourceRefreshAllowed(workspace.ToolRequest{Tool: "read_file", Path: "a.go", StartLine: 1, EndLine: 20}, afterRejectedPlan, 0) {
		t.Fatal("later invalid-anchor submit_plan rejection should reopen its advertised bounded source refresh despite an earlier malformed submit_plan")
	}
}

func TestPlanExecuteTerminalTextualHistoryPrunesReadToolRequests(t *testing.T) {
	messages := []llama.ChatMessage{
		{Role: "system", Content: "base"},
		{Role: "assistant", Content: `{"tool":"read_file","path":"a.go"}`},
		{Role: "system", Content: "Read a.go lines 1-20"},
		{Role: "assistant", Content: `{"tool":"code_navigate","path":"a.go","capability":"read_symbol","pattern":"Run"}`},
		{Role: "assistant", Content: `{"tool":"submit_plan","rationale":"ok","edits":[]}`},
		{Role: "assistant", Content: `{"tool":"read_file","path":"a.go""start_line":1}`},
	}

	pruned, dropped := prunePlanThenExecuteTextualToolRequestHistory(messages)
	if dropped != 3 {
		t.Fatalf("dropped = %d, want 3; pruned=%#v", dropped, pruned)
	}
	joined := ""
	for _, message := range pruned {
		joined += message.Content + "\n"
	}
	if strings.Contains(joined, `"tool":"read_file"`) || strings.Contains(joined, `"tool":"code_navigate"`) {
		t.Fatalf("terminal history should drop prior read/navigation JSON requests, got:\n%s", joined)
	}
	if !strings.Contains(joined, "Read a.go lines") {
		t.Fatalf("terminal history must keep tool result evidence, got:\n%s", joined)
	}
	if !strings.Contains(joined, `"tool":"submit_plan"`) {
		t.Fatalf("terminal history should keep submit_plan attempts for correction, got:\n%s", joined)
	}
}

func TestPlanExecuteReadyToSubmitRaisesBudgetAfterSourceReadCap(t *testing.T) {
	thinkingOff := false
	opts := planThenExecuteReadyToSubmitChatOptions(llama.ChatOptions{
		EnableThinking:   &thinkingOff,
		PreserveThinking: false,
		MaxTokens:        768,
		Tools:            []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
		ToolChoice:       "required",
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go"},
	}, []workspace.ToolResult{
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640"},
		{Tool: "read_file", Summary: "Read f2 cmd/idiom/product_workflow_test.go lines 5257-5270"},
	})

	if opts.MaxTokens != 4096 {
		t.Fatalf("ready-to-submit should raise inherited source-read budget to planner budget, got %d", opts.MaxTokens)
	}
	if opts.EnableThinking == nil || *opts.EnableThinking {
		t.Fatalf("ready-to-submit should keep thinking disabled after source-read cap, got %#v", opts.EnableThinking)
	}
}

func TestPlanExecuteReadyToSubmitUsesLargeBudgetForFourExistingFiles(t *testing.T) {
	thinkingOff := false
	opts := planThenExecuteReadyToSubmitChatOptions(llama.ChatOptions{
		EnableThinking:   &thinkingOff,
		PreserveThinking: false,
		MaxTokens:        4096,
		Tools:            []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
		ToolChoice:       "required",
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go", "internal/productworkflow/coverage_gate.go", "internal/productworkflow/coverage_gate_test.go"},
	}, []workspace.ToolResult{
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read TestProductTDDCoverageGate in cmd/idiom/product_workflow_test.go lines 5257-5270"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read EvaluateCoverageGate in internal/productworkflow/coverage_gate.go lines 1-120"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read TestCoverageGate in internal/productworkflow/coverage_gate_test.go lines 1-80"},
	})

	if opts.MaxTokens != 16384 {
		t.Fatalf("ready-to-submit should use large budget for four-file textual submit_plan output, got %d", opts.MaxTokens)
	}
	if opts.EnableThinking == nil || *opts.EnableThinking {
		t.Fatalf("ready-to-submit should keep thinking disabled for submit_plan-only multi-file synthesis, got %#v", opts.EnableThinking)
	}
}

func TestPlanExecuteReadyToSubmitKeepsBoundedBudgetForThreeExistingFiles(t *testing.T) {
	thinkingOff := false
	opts := planThenExecuteReadyToSubmitChatOptions(llama.ChatOptions{
		EnableThinking:   &thinkingOff,
		PreserveThinking: false,
		MaxTokens:        4096,
		Tools:            []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
		ToolChoice:       "required",
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go", "internal/productworkflow/coverage_gate.go"},
	}, []workspace.ToolResult{
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read TestProductTDDCoverageGate in cmd/idiom/product_workflow_test.go lines 5257-5270"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read EvaluateCoverageGate in internal/productworkflow/coverage_gate.go lines 1-120"},
	})

	if opts.MaxTokens != 8192 {
		t.Fatalf("ready-to-submit should keep bounded budget for three existing files, got %d", opts.MaxTokens)
	}
}

func TestPlanExecuteParseRecoveryUsesLargeBudgetForFourRequiredPaths(t *testing.T) {
	thinkingOff := false
	opts := planThenExecuteRequiredWriteOnlyRecoveryChatOptions(llama.ChatOptions{
		EnableThinking:   &thinkingOff,
		PreserveThinking: false,
		MaxTokens:        8192,
		Tools:            []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
		ToolChoice:       "required",
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go", "internal/productworkflow/coverage_gate.go", "internal/productworkflow/coverage_gate_test.go"},
	}, []workspace.ToolResult{
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read TestProductTDDCoverageGate in cmd/idiom/product_workflow_test.go lines 5257-5270"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read EvaluateCoverageGate in internal/productworkflow/coverage_gate.go lines 1-120"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read TestCoverageGate in internal/productworkflow/coverage_gate_test.go lines 1-80"},
		{Tool: "tool_parse", Summary: "Malformed tool request rejected", Error: "decode tool request: unexpected end of JSON input"},
	})

	if opts.MaxTokens != 16384 {
		t.Fatalf("four-path parse recovery should raise submit_plan retry budget above truncation ceiling, got %d", opts.MaxTokens)
	}
	if opts.ToolChoice != "required" || len(opts.Tools) != 1 || !chatOptionsExposeTool(opts, "submit_plan") {
		t.Fatalf("parse recovery should still expose only submit_plan, got tool_choice=%q tools=%#v", opts.ToolChoice, opts.Tools)
	}
}

func TestPlanExecuteReadyToSubmitKeepsLargeBudgetForNewFileOrWholeFile(t *testing.T) {
	thinkingOff := false
	for name, brief := range map[string]workerruntime.WorkerRuntimeBrief{
		"new-file objective": {
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			Objective:         "Create new file internal/productworkflow/coverage_gate_report.go",
			RequiredEditPaths: []string{"internal/productworkflow/coverage_gate_report.go", "internal/productworkflow/coverage_gate_report_test.go"},
			RequiredEditSnippets: []workerruntime.RequiredEditSnippet{
				{Path: "internal/productworkflow/coverage_gate_report.go", Content: "package productworkflow\n"},
				{Path: "internal/productworkflow/coverage_gate_report_test.go", Content: "package productworkflow\n"},
			},
		},
		"whole-file objective": {
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			Objective:         "Replace whole-file contents for generated coverage report fixtures",
			RequiredEditPaths: []string{"internal/productworkflow/coverage_gate_report.go", "internal/productworkflow/coverage_gate_report_test.go"},
			RequiredEditSnippets: []workerruntime.RequiredEditSnippet{
				{Path: "internal/productworkflow/coverage_gate_report.go", Content: "package productworkflow\n"},
				{Path: "internal/productworkflow/coverage_gate_report_test.go", Content: "package productworkflow\n"},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			opts := planThenExecuteReadyToSubmitChatOptions(llama.ChatOptions{
				EnableThinking:   &thinkingOff,
				PreserveThinking: false,
				MaxTokens:        4096,
				Tools:            []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
				ToolChoice:       "required",
			}, brief, []workspace.ToolResult{
				{Tool: "read_file", Summary: "Read f1 internal/productworkflow/coverage_gate_report.go", Error: "path is a NEW FILE and does not exist yet"},
				{Tool: "read_file", Summary: "Read f2 internal/productworkflow/coverage_gate_report_test.go", Error: "path is a NEW FILE and does not exist yet"},
			})

			if opts.MaxTokens != 16384 {
				t.Fatalf("large-body/new-file submit_plan should preserve full budget, got %d", opts.MaxTokens)
			}
		})
	}
}

func TestPlanExecuteRejectedPlanCorrectionUsesLargeBudgetForFourExistingFiles(t *testing.T) {
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
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go", "internal/productworkflow/coverage_gate.go", "internal/productworkflow/coverage_gate_test.go"},
	}, []workspace.ToolResult{
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read TestProductTDDCoverageGate in cmd/idiom/product_workflow_test.go lines 5257-5270"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read EvaluateCoverageGate in internal/productworkflow/coverage_gate.go lines 1-120"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read TestCoverageGate in internal/productworkflow/coverage_gate_test.go lines 1-80"},
		{Tool: "submit_plan", Summary: "submit_plan rejected: required edit path coverage mismatch", Error: "missing required edit path(s): `cmd/idiom/product_workflow_test.go`, `internal/productworkflow/coverage_gate.go`, `internal/productworkflow/coverage_gate_test.go`; invalid edit anchor(s): `cmd/idiom/product_workflow.go: old_text not found in current source`; placeholder edit body path(s): `cmd/idiom/product_workflow.go: old_text contains placeholder marker \"... existing code ...\"`"},
		{Tool: "read_file", Summary: "Read f2 cmd/idiom/product_workflow.go lines 1396-1671"},
	})

	if opts.MaxTokens != 16384 {
		t.Fatalf("correction-only four-file submit_plan recovery should use large budget, got %d", opts.MaxTokens)
	}
	if opts.ToolChoice != "required" || len(opts.Tools) != 1 || !chatOptionsExposeTool(opts, "submit_plan") {
		t.Fatalf("correction-only recovery should expose only submit_plan, got tool_choice=%q tools=%#v", opts.ToolChoice, opts.Tools)
	}
}

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
		{Tool: "read_file", Summary: "Read f1 internal/productworkflow/coverage_gate.go lines 1-391", Output: "package productworkflow\n"},
		{Tool: "read_file", Summary: "Read f2 cmd/idiom/product_workflow.go lines 1-180", Output: "package main\n"},
		{Tool: "submit_plan", Summary: "submit_plan rejected", Error: "edits[0]: edit would not change internal/productworkflow/coverage_gate.go; submit real edits, or answer `Decision: source-satisfied` with current-state evidence when no changes are needed"},
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
		RequiredEditPaths: []string{"internal/productworkflow/coverage_gate_report.go", "internal/productworkflow/coverage_gate_report_test.go", "internal/productworkflow/coverage_gate_fixture.json"},
	}, []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 internal/productworkflow/coverage_gate_report.go lines 1-40"},
		{Tool: "read_file", Summary: "Read f2 internal/productworkflow/coverage_gate_report_test.go lines 1-40"},
		{Tool: "read_file", Summary: "Read f3 internal/productworkflow/coverage_gate_fixture.json lines 1-80"},
		{Tool: "submit_plan", Summary: "submit_plan rejected: invalid edit shape", Error: "replace_all requires a full file body for internal/productworkflow/coverage_gate_fixture.json"},
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
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go", "internal/productworkflow/coverage_gate.go", "internal/productworkflow/coverage_gate_test.go"},
	}, []workspace.ToolResult{
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read TestProductTDDCoverageGate in cmd/idiom/product_workflow_test.go lines 5257-5270"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read EvaluateCoverageGate in internal/productworkflow/coverage_gate.go lines 1-120"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read TestCoverageGate in internal/productworkflow/coverage_gate_test.go lines 1-80"},
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
	}, []workspace.ToolResult{{Tool: "read_file", Summary: "Read f1 internal/app/rendering.go lines 1-120"}})

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
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/productworkflow/coverage_gate.go"},
	}, []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 cmd/idiom/product_workflow.go lines 1-20"},
		{Tool: "read_file", Summary: "Read f2 internal/productworkflow/coverage_gate.go lines 1-20"},
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

func TestPlanExecuteRejectedMissingSourceGroundingRequiresRefreshForRejectedEditPath(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
	}
	rejected := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected: edit paths lack source grounding",
		Error:   "submit_plan edits for plan-execute planner atoms must be grounded before executor handoff; missing source-body evidence for `internal/productworkflow/tdd.go`. Read each existing edit path with `read_file` or `code_navigate` `read_symbol`/`extract_body`, then submit a corrected plan.",
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 cmd/idiom/product_workflow.go lines 1-120 (total lines: 2894)"},
		rejected,
	}
	if !planThenExecuteRejectedPlanNeedsSourceRefresh(brief, results) {
		t.Fatal("missing source-body grounding rejection should enable a bounded refresh for the rejected edit path")
	}
	paths := planThenExecuteRejectedPlanRefreshPaths(brief, results, rejected, 1)
	if len(paths) != 1 || paths[0] != "internal/productworkflow/tdd.go" {
		t.Fatalf("refresh paths = %#v, want rejected edit path", paths)
	}
	results = append(results, workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read f2 internal/productworkflow/tdd.go lines 1-50 (total lines: 120)",
	})
	if planThenExecuteRejectedPlanNeedsSourceRefresh(brief, results) {
		t.Fatal("successful source read after the rejected plan should close missing-grounding refresh")
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
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/productworkflow/coverage_gate.go"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 cmd/idiom/product_workflow.go lines 1-20"},
		{Tool: "read_file", Summary: "Read f2 internal/productworkflow/coverage_gate.go lines 1-20"},
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

func TestPlanExecuteSourceRefreshRecoveryClearsStaleSubmitOnlySurface(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"internal/productworkflow/coverage_gate.go", "internal/productworkflow/coverage_gate_test.go"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 internal/productworkflow/coverage_gate.go lines 1-80 (total lines: 120)"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: edit paths lack source grounding",
			Error:   "submit_plan edits for plan-execute planner atoms must be grounded before executor handoff; missing source-body evidence for `internal/productworkflow/coverage_gate_test.go`. Read each existing edit path with `read_file` or `code_navigate` `read_symbol`/`extract_body`, then submit a corrected plan.",
		},
	}
	opts := planThenExecutePostReadinessRecoveryChatOptionsForBackend(llama.ChatOptions{
		Tools:      []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
		ToolChoice: "auto",
	}, brief, results, true)
	if chatOptionsExposeTool(opts, "submit_plan") || opts.ToolChoice != "" || opts.ForceNativeTools || opts.ResponseFormatJSON {
		t.Fatalf("source-refresh recovery must clear stale submit-only surface, got force=%v json=%v choice=%q tools=%#v", opts.ForceNativeTools, opts.ResponseFormatJSON, opts.ToolChoice, opts.Tools)
	}
	if opts.MaxTokens != 768 {
		t.Fatalf("source-refresh recovery should clamp retry budget to 768 tokens, got %d", opts.MaxTokens)
	}
}

func TestPlanExecutePostReadinessRecoveryAllowsCandidateAnchorRefresh(t *testing.T) {
	thinkingOn := true
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedEditPaths:  []string{"cmd/idiom/product_workflow.go", "internal/productworkflow/coverage_gate.go"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 internal/productworkflow/coverage_gate.go lines 1-360 (total lines: 360)"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "invalid edit anchor(s): `internal/productworkflow/coverage_gate.go: old_text not found in current source`, `cmd/idiom/product_workflow.go: old_text not found in current source`.",
		},
	}
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
	}, brief, results)
	if opts.ToolChoice != "required" {
		t.Fatalf("candidate anchor-refresh recovery should force a parser-readable tool request, got %q", opts.ToolChoice)
	}
	if len(opts.Tools) != 2 || !chatOptionsExposeTool(opts, "read_file") || !chatOptionsExposeTool(opts, "code_navigate") || chatOptionsExposeTool(opts, "search_content") || chatOptionsExposeTool(opts, "submit_plan") {
		t.Fatalf("candidate anchor-refresh recovery should expose only source-body read tools, got %#v", opts.Tools)
	}
	if opts.MaxTokens > 768 {
		t.Fatalf("candidate anchor-refresh recovery should cap retry budget, got %d", opts.MaxTokens)
	}
}

func TestPlanExecutePostReadinessRecoveryAllowsSyntaxPreflightRefresh(t *testing.T) {
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
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go"},
	}, []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 cmd/idiom/product_workflow.go lines 80-125"},
		{Tool: "read_file", Summary: "Read f2 cmd/idiom/product_workflow_test.go lines 1-80"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "edits[0]: preflight syntax validation failed for cmd/idiom/product_workflow.go at line 100: ./cmd/idiom/product_workflow.go:100:6: expected '(', found runProductValidateArtifact",
		},
	})

	if opts.ToolChoice != "required" {
		t.Fatalf("syntax-refresh recovery should force a parser-readable tool request, got %q", opts.ToolChoice)
	}
	if len(opts.Tools) != 2 || !chatOptionsExposeTool(opts, "read_file") || !chatOptionsExposeTool(opts, "code_navigate") || chatOptionsExposeTool(opts, "search_content") || chatOptionsExposeTool(opts, "submit_plan") {
		t.Fatalf("syntax-refresh recovery should expose only source-body read tools, got %#v", opts.Tools)
	}
	if opts.EnableThinking == nil || *opts.EnableThinking {
		t.Fatalf("syntax-refresh recovery should force visible/tool output over hidden thinking, got %#v", opts.EnableThinking)
	}
}

func TestPlanExecutePostReadinessRecoveryForcesCorrectedSubmitPlanAfterAnchorRefresh(t *testing.T) {
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
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/productworkflow/coverage_gate.go"},
	}, []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 cmd/idiom/product_workflow.go lines 1-20"},
		{Tool: "read_file", Summary: "Read f2 internal/productworkflow/coverage_gate.go lines 1-20"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "invalid edit anchor(s): `cmd/idiom/product_workflow.go: old_text not found in current source`.",
		},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640"},
	})

	if opts.ToolChoice != "required" {
		t.Fatalf("corrected-plan recovery should require submit_plan after anchor refresh, got %q", opts.ToolChoice)
	}
	if len(opts.Tools) != 1 || !chatOptionsExposeTool(opts, "submit_plan") {
		t.Fatalf("corrected-plan recovery should expose only submit_plan after anchor refresh, got %#v", opts.Tools)
	}
}

func TestPlanExecuteRuntimeRecoverySequencePreservesNewFilePlannerBudget(t *testing.T) {
	thinkingOn := true
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		Objective:         "Create internal/writerroute/decision.go",
		RequiredEditPaths: []string{"internal/writerroute/decision.go"},
	}
	results := []workspace.ToolResult{{Tool: "read_file", Summary: "Read f1 internal/writerroute/decision.go", Error: "path is a NEW FILE and does not exist yet"}}
	opts := completionGateRecoveryChatOptions(llama.ChatOptions{
		EnableThinking:     &thinkingOn,
		PreserveThinking:   true,
		ChatTemplateKwargs: false,
		MaxTokens:          16384,
		Tools:              []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
		ToolChoice:         "auto",
	}, brief, results)
	opts = planThenExecuteRequiredWriteOnlyRecoveryChatOptions(opts, brief, results)

	if opts.MaxTokens != 16384 {
		t.Fatalf("real recovery option sequence should preserve new-file planner budget, got %d", opts.MaxTokens)
	}
}

func TestReasoningSubmitPlanRejectsOldTextOnlyJSONEdit(t *testing.T) {
	req, err := workspace.DecodeToolRequestJSON([]byte(`{"tool":"submit_plan","edits":[{"path":"a.txt","old_text":"old"}]}`))
	if err != nil {
		t.Fatalf("DecodeToolRequestJSON: %v", err)
	}
	if reasoningSubmitPlanHasConcreteEdit(req) {
		t.Fatal("old_text-only JSON submit_plan must not be treated as a concrete visible edit")
	}
}

func TestPlanExecuteCandidateWriteOnlyRecoveryDoesNotForceWithoutReadEvidence(t *testing.T) {
	opts := planThenExecuteCandidateWriteOnlyRecoveryChatOptions(llama.ChatOptions{
		MaxTokens:  2048,
		Tools:      []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
		ToolChoice: "auto",
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
	}, []workspace.ToolResult{{Tool: "context_pack", Summary: "candidate packet"}})

	if opts.ToolChoice != "auto" {
		t.Fatalf("candidate recovery without a source read should not force tool choice, got %q", opts.ToolChoice)
	}
	if opts.MaxTokens != 2048 {
		t.Fatalf("candidate recovery without a source read should preserve budget, got %d", opts.MaxTokens)
	}
}

func TestReadPathsFromResultsCountsCodeNavigateRouteSummary(t *testing.T) {
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output: strings.Join([]string{
				"capability: read_symbol",
				"language: go",
				"routed_tool: go_read_symbol",
				"route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1382-1501",
				"",
				"1382:func runProductTDD(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {",
			}, "\n"),
		},
		{
			Tool:    "read_file",
			Summary: "Read f2 cmd/idiom/product_workflow_test.go lines 3209-3230",
		},
	}

	paths := readPathsFromResults(results)
	for _, want := range []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go"} {
		if _, ok := paths[want]; !ok {
			t.Fatalf("read paths missing %q: %#v", want, paths)
		}
	}
}

func TestReadPathsFromResultsDoesNotCountRouteSummaryWithoutSourceBody(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output: strings.Join([]string{
			"capability: read_symbol",
			"language: go",
			"routed_tool: go_read_symbol",
			"route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1382-1501",
		}, "\n"),
	}}

	paths := readPathsFromResults(results)
	if _, ok := paths["cmd/idiom/product_workflow.go"]; ok {
		t.Fatalf("route_summary without source body should not satisfy source readiness: %#v", paths)
	}
}

const planExecuteStatusMigrationApplyTUIObjective = "We are continuing TDD completion work from docs/design-prd-adr-brief-research-workflow.md. Implement one bounded topic only: status migration apply must harden predictable temp-path writes against pre-existing symlinks. Focus on the existing product workflow status migration apply path. Add regression tests proving pre-existing workflow/index/events temp symlinks are rejected without partial durable mutation. Use repo discovery first, make the minimal code changes, and run the focused tests."

func TestLiteralRecoverySearchPatternPrioritizesTopicOverWorkflowBoilerplate(t *testing.T) {
	got := literalRecoverySearchPattern(planExecuteStatusMigrationApplyTUIObjective)
	for _, want := range []string{"status", "migration", "apply", "temp"} {
		if !strings.Contains(got, want) {
			t.Fatalf("recovery search pattern missing %q: %q", want, got)
		}
	}
	for _, notWant := range []string{"tdd", "prd", "adr", "brief", "research", "workflow"} {
		if strings.Contains(got, notWant) {
			t.Fatalf("recovery search pattern should not spend early terms on workflow boilerplate %q: %q", notWant, got)
		}
	}
}

func TestCompletionGateRecoveryPromptKeepsUnreadDiscoveredCandidateReadVisible(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			Objective:         planExecuteStatusMigrationApplyTUIObjective,
		},
	}

	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote .idiom/comprehension/views/test.json with 2 candidate file(s)",
			Output:  `{"candidate_files":[{"path":"internal/productworkflow/migration.go"},{"path":"internal/productworkflow/migration_apply.go"}]}`,
		},
		{
			Tool:    "search_content",
			Summary: "Found matching lines.",
			Output: strings.Join([]string{
				`internal/productworkflow/migration.go:34: // status migration planning metadata`,
				`cmd/idiom/product_workflow.go:65: idiom product status-migration --apply updates workflow_state, events, and index`,
				`internal/productworkflow/migration_apply.go:88: tmpPath := workflowPath + ".tmp"`,
				`internal/productworkflow/migration_apply.go:92: if err := rejectSymlinkTempPath(tmpPath); err != nil {`,
			}, "\n"),
		},
		{
			Tool:    "read_file",
			Summary: "Read f97 internal/productworkflow/migration.go lines 1-180 (total lines: 180)",
			Output:  "type StatusMigrationPlan struct{}",
		},
	}
	prompt, hideReads, ok := runner.completionGateRecoveryPrompt(results)
	if !ok {
		t.Fatalf("expected candidate-scope recovery prompt")
	}
	if hideReads {
		t.Fatalf("candidate-scope recovery should keep bounded reads visible when other surfaced candidate paths remain unread:\n%s", prompt)
	}
	for _, want := range []string{
		"Allowed/candidate paths are not coverage obligations",
		"submit_plan",
		"Read candidate path(s): internal/productworkflow/migration.go",
		"read one different unread candidate path",
		"internal/productworkflow/migration_apply.go",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("recovery prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestPlanExecuteCandidateScopeReadinessDoesNotDeferForUnreadBetterCandidate(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		Objective:         planExecuteStatusMigrationApplyTUIObjective,
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote .idiom/comprehension/views/test.json with 2 candidate file(s)",
			Output:  `{"candidate_files":[{"path":"internal/productworkflow/migration.go"},{"path":"internal/productworkflow/migration_apply.go"}]}`,
		},
		{
			Tool:    "search_content",
			Summary: "Found matching lines.",
			Output: strings.Join([]string{
				`internal/productworkflow/migration.go:34: // status migration planning metadata`,
				`cmd/idiom/product_workflow.go:65: idiom product status-migration --apply updates workflow_state, events, and index`,
				`internal/productworkflow/migration_apply.go:88: tmpPath := workflowPath + ".tmp"`,
				`internal/productworkflow/migration_apply.go:92: if err := rejectSymlinkTempPath(tmpPath); err != nil {`,
			}, "\n"),
		},
		{
			Tool:    "read_file",
			Summary: "Read f97 internal/productworkflow/migration.go lines 1-180",
			Output:  "type StatusMigrationPlan struct{}",
		},
	}
	got := planThenExecuteReadyToSubmitNudge(brief, results)
	if got == "" {
		t.Fatal("unscoped candidate evidence should create submit-ready guidance without requiring every ranked candidate")
	}
	for _, want := range []string{
		"internal/productworkflow/migration.go",
		"Allowed/candidate paths are not coverage obligations",
		"One bounded source-read/search for a different candidate path",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("readiness nudge missing %q:\n%s", want, got)
		}
	}
}

func TestPlanExecuteCandidateScopeReadinessDoesNotDeferForUnreadAllowedPath(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:       PlanThenExecutePlannerTaskFamily,
		Contract:         "implementation_result",
		AllowedEditPaths: []string{"pkg/feature.go", "pkg/feature_test.go"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 pkg/feature.go lines 1-20"},
	}
	if got := planThenExecuteReadyToSubmitNudge(brief, results); got != "" {
		if !strings.Contains(got, "PLANNER READINESS REACHED") {
			t.Fatalf("allowed candidate evidence should close discovery without reading every allowed path:\n%s", got)
		}
		if strings.Contains(got, "pkg/feature_test.go") {
			t.Fatalf("nudge should not promote unread allowed paths to coverage obligations:\n%s", got)
		}
		return
	}
	t.Fatal("expected candidate-scope readiness after one allowed candidate source read")
}

func TestPlanThenExecuteVisibleBriefKeepsSubmitPlanContract(t *testing.T) {
	brief := runnerVisibleBriefForToolSurface(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		Constraints:       []string{"implementation_packet_write_ready"},
		AllowedTools:      []string{"read_file", "code_navigate", "submit_plan", "edit_file", "apply_patch", "harness_feedback"},
		RequiredTools:     []string{"submit_plan"},
		ReadinessGate:     []string{"submit_plan_called_or_blocker_reported"},
		RequiredEditPaths: []string{"a.txt", "b.txt"},
		TargetPacket:      []string{"verified anchors"},
	}, nil)

	if !slices.Contains(brief.AllowedTools, "submit_plan") {
		t.Fatalf("plan-execute planner surface must keep submit_plan, got %#v", brief.AllowedTools)
	}
	if !slices.Contains(brief.AllowedTools, "harness_feedback") {
		t.Fatalf("plan-execute planner surface should not be narrowed by implementation scoped-edit filters, got %#v", brief.AllowedTools)
	}
	if strings.Join(brief.RequiredTools, "\n") != "submit_plan" {
		t.Fatalf("plan-execute planner required tools = %#v, want submit_plan only", brief.RequiredTools)
	}
}

func TestPlanThenExecutePlannerSurfaceKeepsProductWorkflowBrowsers(t *testing.T) {
	root := t.TempDir()
	manager := workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true})
	manager.SetPlanThenExecutePlannerMode(true)
	runner := &Runner{
		Workspace: manager,
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"recovery_inspect", "recovery_status", "product_efforts", "product_status", "read_file", "submit_plan"},
			RequiredTools:     []string{"submit_plan"},
		},
	}

	allowed := runner.advertisedAllowedTools(manager.AllowedTools())
	for _, tool := range []string{"recovery_inspect", "recovery_status", "product_efforts", "product_status"} {
		if !slices.Contains(allowed, tool) {
			t.Fatalf("planner surface lost %s; allowed=%#v", tool, allowed)
		}
		if summary, errorText, reject := currentToolSurfaceRejection(workspace.ToolRequest{Tool: tool}, allowed); reject {
			t.Fatalf("%s should be callable in plan-execute planner surface, summary=%q error=%q allowed=%#v", tool, summary, errorText, allowed)
		}
	}
	if slices.Contains(allowed, "recovery_decompose") {
		t.Fatalf("planner surface must not expose recovery_decompose because it writes recovery packets, allowed=%#v", allowed)
	}
}

func TestPlanThenExecuteControlRailsKeepSubmitPlanContract(t *testing.T) {
	root := t.TempDir()
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "code_navigate", "submit_plan", "edit_file", "apply_patch"},
			RequiredTools:     []string{"submit_plan"},
			ReadinessGate:     []string{"submit_plan_called_or_blocker_reported"},
			SearchPolicy:      []string{"submit_plan_when_edit_set_is_supported"},
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}

	rails := runner.controlRails(nil)
	if !slices.Contains(rails.RequiredTools, "submit_plan") {
		t.Fatalf("control rails must require submit_plan for planner phase, got %#v", rails.RequiredTools)
	}
	if slices.Contains(rails.RequiredTools, "edit_file_or_apply_patch") {
		t.Fatalf("control rails must not advertise per-edit write requirement in planner phase: %#v", rails.RequiredTools)
	}
	if !slices.Contains(rails.ReadinessGate, "submit_plan_or_source_satisfied_or_blocker_reported") {
		t.Fatalf("control rails must keep submit_plan readiness gate, got %#v", rails.ReadinessGate)
	}
	if !slices.Contains(rails.SearchPolicy, "required_existing_paths_need_source_body_before_submit_plan") {
		t.Fatalf("control rails must advertise source-body-before-submit_plan policy, got %#v", rails.SearchPolicy)
	}
	if slices.Contains(rails.SearchPolicy, "trust_preloaded_current_state_anchors") {
		t.Fatalf("control rails must not imply preloaded anchors satisfy source-body evidence, got %#v", rails.SearchPolicy)
	}
	if !slices.Contains(rails.FinalCritic, "submit_plan_called") ||
		!slices.Contains(rails.FinalCritic, "source_satisfied_or_blocker_only_with_per_path_evidence") {
		t.Fatalf("control rails must keep submit_plan final critic, got %#v", rails.FinalCritic)
	}
}

func TestPlanThenExecuteRequiredPathHintsUseSubmitPlanLanguage(t *testing.T) {
	brief := runnerBriefWithPhaseHints(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredTools:     []string{"submit_plan"},
		RequiredEditPaths: []string{"a.txt", "b.txt"},
		TargetPacket:      []string{"verified anchors"},
	}, nil)

	joined := strings.Join(append(append([]string{}, brief.RequiredNext...), brief.InvalidActions...), "\n")
	if !strings.Contains(joined, "submit_plan") {
		t.Fatalf("planner-phase hints must name submit_plan, got %#v / %#v", brief.RequiredNext, brief.InvalidActions)
	}
	if !strings.Contains(joined, "read required_edit_path a.txt with source-body evidence before submit_plan") {
		t.Fatalf("planner-phase hints must frontload unread required-path source evidence, got %#v / %#v", brief.RequiredNext, brief.InvalidActions)
	}
	if !strings.Contains(joined, "submit_plan before every existing required_edit_path has satisfied its source-body obligation") {
		t.Fatalf("planner-phase hints must reject premature submit_plan, got %#v / %#v", brief.RequiredNext, brief.InvalidActions)
	}
	if !strings.Contains(joined, "source-satisfied") {
		t.Fatalf("planner-phase hints must name source-satisfied closeout, got %#v / %#v", brief.RequiredNext, brief.InvalidActions)
	}
	if strings.Contains(joined, "write capability") || strings.Contains(joined, "first required-path write") {
		t.Fatalf("planner-phase hints must not use generic write/edit closeout language: %s", joined)
	}
}

func TestPlanThenExecuteRequiredPathHintsSwitchToSubmitPlanAfterSourceEvidence(t *testing.T) {
	brief := runnerBriefWithPhaseHints(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredTools:     []string{"submit_plan"},
		RequiredEditPaths: []string{"a.txt", "b.txt"},
		TargetPacket:      []string{"verified anchors"},
	}, []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.txt lines 1-10 (total lines: 10)", Output: "current a"},
		{Tool: "read_file", Summary: "Read f2 b.txt lines 1-10 (total lines: 10)", Output: "current b"},
	})

	joined := strings.Join(append(append([]string{}, brief.RequiredNext...), brief.InvalidActions...), "\n")
	if !strings.Contains(joined, "submit_plan with one ordered edit entry for every required_edit_paths entry") {
		t.Fatalf("planner-phase hints should require submit_plan once required paths are read, got %#v / %#v", brief.RequiredNext, brief.InvalidActions)
	}
	if strings.Contains(joined, "read required_edit_path a.txt with source-body evidence before submit_plan") {
		t.Fatalf("planner-phase hints should not keep source-read-first language after every required path is read: %s", joined)
	}
}

func TestPlanThenExecuteRequiredPathHintsClearStaleSourceReadLanguageAfterReadiness(t *testing.T) {
	brief := runnerBriefWithPhaseHints(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredTools:     []string{"submit_plan"},
		RequiredEditPaths: []string{"a.txt", "b.txt"},
		RequiredNext: []string{
			"read required_edit_path a.txt with source-body evidence before submit_plan; after every existing required_edit_path has satisfied its source-body obligation, submit_plan with one ordered edit entry for every required path",
		},
		InvalidActions: []string{
			"submit_plan before every existing required_edit_path has satisfied its source-body obligation",
			"new orientation read/search before the next unread required_edit_path source-body read",
		},
		SearchPolicy: []string{
			"progress through unread required_edit_paths one source-body read at a time before submit_plan",
			"keep this unrelated policy",
		},
	}, []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.txt lines 1-10 (total lines: 10)", Output: "current a"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read B in b.txt lines 1-10\n1: current b"},
	})

	joined := strings.Join(append(append(append([]string{}, brief.RequiredNext...), brief.InvalidActions...), brief.SearchPolicy...), "\n")
	if !strings.Contains(joined, "submit_plan with one ordered edit entry for every required_edit_paths entry") {
		t.Fatalf("planner-phase hints should require submit_plan once source evidence is complete, got %s", joined)
	}
	for _, stale := range []string{
		"read required_edit_path a.txt",
		"submit_plan before every existing required_edit_path has satisfied",
		"new orientation read/search before the next unread required_edit_path",
		"progress through unread required_edit_paths",
	} {
		if strings.Contains(joined, stale) {
			t.Fatalf("planner-phase hints retained stale source-read language %q: %s", stale, joined)
		}
	}
	if !strings.Contains(joined, "keep this unrelated policy") {
		t.Fatalf("planner-phase hint cleanup should preserve unrelated policy, got %s", joined)
	}
}

func TestPlanExecuteReadinessRecoverySyncsTextualToolSurfaceWithSubmitPlanOnly(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte("before\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	mgr := workspace.NewManager(config.Workspace{
		Root:         root,
		CWD:          root,
		EnableWrites: true,
	})
	mgr.SetPlanThenExecutePlannerMode(true)
	if !slices.Contains(mgr.AllowedTools(), "harness_feedback") {
		t.Fatalf("test requires harness_feedback to be present in the broad planner surface")
	}

	replies := []string{
		`{"tool":"read_file","path":"a.txt"}`,
		`{"tool":"read_file","path":"b.txt"}`,
		`{"tool":"harness_feedback","category":"tooling","component":"plan-execute","observed":"stale textual allow-list admitted a side channel","expected":"submit_plan only","impact":"planner recovery can drift away from the required visible action","confidence":"high"}`,
		`{"tool":"submit_plan","rationale":"Replace both required fixture files.","edits":[{"path":"a.txt","old_text":"before\n","new_text":"after a\n","purpose":"replace a fixture"},{"path":"b.txt","old_text":"before\n","new_text":"after b\n","purpose":"replace b fixture"}]}`,
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

	var executedFeedback bool
	runner := &Runner{
		Client:    llama.NewClient(server.URL, time.Second),
		Workspace: mgr,
		Policy:    RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}
	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan edits"}}, llama.ChatOptions{
		MaxTokens: 2048,
		Tools:     buildLlamaTools(mgr.BuildToolDefinitions(mgr.AllowedTools())),
	}, nil, func(event ToolRequestEvent) {
		if event.Request.Tool == "harness_feedback" {
			executedFeedback = true
		}
	}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if executedFeedback {
		t.Fatalf("harness_feedback executed after readiness; textual allow-list should have been synced to submit_plan only")
	}
	if !strings.Contains(answer, "Plan submitted with 2 edit") {
		t.Fatalf("expected successful plan after rejecting side-channel tool, got:\n%s", answer)
	}
}

func TestPlanThenExecuteRequiredPathHintsDoNotReadTerminalMissingPath(t *testing.T) {
	brief := runnerBriefWithPhaseHints(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredTools:     []string{"submit_plan"},
		RequiredEditPaths: []string{"new_file.go"},
	}, []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "read_file failed for new_file.go",
			Error:   `path "new_file.go" is in your required_edit_paths but does not exist on disk yet - this is a NEW FILE you must create.`,
		},
	})

	joined := strings.Join(append(append([]string{}, brief.RequiredNext...), brief.InvalidActions...), "\n")
	if !strings.Contains(joined, "submit_plan with one ordered edit entry for every required_edit_paths entry") {
		t.Fatalf("terminal missing-file evidence should move planner toward submit_plan/question, got %#v / %#v", brief.RequiredNext, brief.InvalidActions)
	}
	if strings.Contains(joined, "read required_edit_path new_file.go with source-body evidence before submit_plan") {
		t.Fatalf("terminal missing-file evidence should not keep read-first language: %s", joined)
	}
}

func TestPlanThenExecuteMissingNewFilePathsBecomeSubmitReadyWithoutMoreReads(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{
			"internal/viewer/doc1.go",
			"internal/viewer/doc2.go",
			"internal/viewer/doc3.go",
			"internal/viewer/doc4.go",
			"internal/viewer/doc5.go",
		},
	}
	var results []workspace.ToolResult
	for _, path := range brief.RequiredEditPaths {
		results = append(results, workspace.ToolResult{
			Tool:    "read_file",
			Summary: "read_file failed for " + path,
			Error:   `path "` + path + `" is in your required_edit_paths but does not exist on disk yet - this is a NEW FILE you must create.`,
		})
	}

	if unread := planThenExecuteUnreadRequiredPaths(brief, results); len(unread) != 0 {
		t.Fatalf("missing required new files should not remain source-body reads after first missing-file proof, got %#v", unread)
	}
	if !planThenExecuteSubmitPlanOnlyReady(brief, results) {
		t.Fatal("all missing required new files should transition to submit-ready/blocker contract")
	}
	if got := planThenExecuteReadyToSubmitNudge(brief, results); !strings.Contains(got, "PLANNER READINESS REACHED") {
		t.Fatalf("missing required new files should get terminal submit-ready nudge, got:\n%s", got)
	}
	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode != PlanExecutePlanningModeSubmitReady {
		t.Fatalf("Mode = %q, want submit_ready; states=%#v", state.Mode, state.RequiredPathStates)
	}
	for _, got := range state.RequiredPathStates {
		if !strings.HasSuffix(got, ":"+string(planThenExecuteRequiredPathNewFileBodyRequired)) {
			t.Fatalf("required path state = %q, want new_file_body_required", got)
		}
	}
	runner := &Runner{RuntimeBrief: brief}
	if req, raw, ok := runner.deterministicPlanExecuteRequiredSourceRead(results); ok {
		t.Fatalf("missing new-file paths should not schedule another source-body read, got req=%#v raw=%s", req, raw)
	}
}

func TestPlanThenExecuteMissingSubmitPlanGateNamesSubmitPlan(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}

	got, rejected := runner.mutationCompletionRejection("Done.", nil)
	if !rejected {
		t.Fatalf("planner phase without submit_plan must be rejected")
	}
	if !strings.Contains(got.Error, "submit_plan") {
		t.Fatalf("planner-phase rejection must name submit_plan, got %#v", got)
	}
	if !strings.Contains(got.Error, "source-satisfied") {
		t.Fatalf("planner-phase rejection must offer source-satisfied closeout, got %#v", got)
	}
	if strings.Contains(got.Error, "no successful write tool result") {
		t.Fatalf("planner-phase rejection must not use generic write-tool language: %#v", got)
	}
}

func TestPlanThenExecuteMissingSubmitPlanGateWithoutRequiredPathsNamesCandidateScope(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}

	got, rejected := runner.mutationCompletionRejection("Done.", nil)
	if !rejected {
		t.Fatalf("planner phase without submit_plan must be rejected")
	}
	if !strings.Contains(got.Error, "candidate/allowed edit path") {
		t.Fatalf("planner-phase rejection without required paths should name candidate scope, got %#v", got)
	}
	if strings.Contains(got.Error, "every required_edit_paths entry") {
		t.Fatalf("planner-phase rejection without required paths should not imply required path coverage, got %#v", got)
	}
}

func TestPlanThenExecuteCandidateCompletionRejectsGenericBlockedProse(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedEditPaths:  []string{"cmd/idiom/product_workflow.go", "internal/productworkflow/coverage_gate.go"},
		},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read file.", Output: `{"path":"cmd/idiom/product_workflow.go","content":"before\n"}`},
		{Tool: "submit_plan", Summary: "Plan rejected", Error: "old_text not found in cmd/idiom/product_workflow.go"},
	}

	got, rejected := runner.mutationCompletionRejection("The planner is blocked because candidate source is already ready but no plan was submitted.", results)
	if !rejected {
		t.Fatalf("candidate plan-execute must reject generic blocked prose without submit_plan, got %#v", got)
	}
	if !strings.Contains(got.Error, "submit_plan") || !strings.Contains(got.Error, "<<QUESTION>>") {
		t.Fatalf("candidate rejection should name the exact planner closeout contract, got %#v", got)
	}
}

func TestPlanThenExecuteCandidateCompletionAllowsTypedBlocker(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedEditPaths:  []string{"cmd/idiom/product_workflow.go", "internal/productworkflow/coverage_gate.go"},
		},
	}

	if got, rejected := runner.mutationCompletionRejection("Decision: block\n\nExact blocker: current source anchor for cmd/idiom/product_workflow.go could not be recovered after the rejected submit_plan.", nil); rejected {
		t.Fatalf("typed candidate blocker should satisfy planner closeout, got %#v", got)
	}
	if got, rejected := runner.mutationCompletionRejection("<<QUESTION>>: exact blocker: current source anchor for cmd/idiom/product_workflow.go could not be recovered after the rejected submit_plan.", nil); rejected {
		t.Fatalf("question-style candidate blocker should satisfy planner closeout, got %#v", got)
	}
}

func TestPlanThenExecuteCandidateCompletionRejectsMentionedDecisionBlock(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedEditPaths:  []string{"cmd/idiom/product_workflow.go"},
		},
	}

	got, rejected := runner.mutationCompletionRejection("I know I could use Decision: block, but I will keep investigating.", nil)
	if !rejected {
		t.Fatalf("mentioning Decision: block in prose must not satisfy typed blocker closeout, got %#v", got)
	}
}

func TestMutationCompletionGateStillRejectsImplementationLaneWithoutEdits(t *testing.T) {
	// Sanity check: my plan-execute exemption must not weaken the gate
	// for normal implementation lanes. A regular implementation that
	// finalizes without any write tool result (and no submit_plan)
	// should still be rejected.
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt"},
		},
	}
	got, rejected := runner.mutationCompletionRejection("Done.", nil)
	if !rejected {
		t.Fatalf("implementation lane with no edits must be rejected; got %#v", got)
	}
}

func TestCompletionGateRecoveryPromptForUnscopedImplementationNoWrite(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}

	prompt, hideReads, ok := runner.completionGateRecoveryPrompt([]workspace.ToolResult{
		{Tool: "context_pack", Summary: "Ranked context packet produced.", Output: `{"candidate_files":[{"path":"candidate/path.go"}]}`},
	})
	if !ok {
		t.Fatalf("expected completion gate recovery prompt for direct implementation with no write")
	}
	if hideReads {
		t.Fatalf("direct implementation recovery should leave bounded reads visible")
	}
	for _, want := range []string{
		"before any successful workspace write",
		"No required_edit_paths list is active",
		"target-selection failure",
		"parser-readable action",
		"not prose",
		"edit_file",
		"write_file",
		"append_file",
		"apply_patch",
		"Do not call a write tool until",
		"previous `context_pack` did not establish an exact edit target",
		"`search_content`",
		"named_gap",
		"why_not_answering",
		"<<QUESTION>>",
		"Do not repeat a final answer",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("direct implementation recovery prompt missing %q: %q", want, prompt)
		}
	}
	if strings.Contains(prompt, "submit_plan") {
		t.Fatalf("direct implementation recovery must not point at planner-phase submit_plan: %q", prompt)
	}
	for _, forbidden := range []string{"Current evidence paths", `"path":"candidate/path.go"`} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("context_pack-only recovery should not treat packet candidates as concrete targets (%q): %q", forbidden, prompt)
		}
	}
}

func TestCompletionGateRecoveryPromptForUnscopedImplementationNoWriteBecomesTerminalAfterRepeatedGate(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read internal/app/rendering.go lines 1-120 (total lines: 332)",
			Output:  "1:package app\n2:func renderMessages() {}\n",
		},
		{
			Tool:    "completion_gate",
			Summary: "Mutation completion gate rejected final answer",
			Error:   "this task asks for a workspace change, but no successful write tool result is present",
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/app/model_state.go lines 1-120 (total lines: 1456)",
			Output:  "1:package app\n2:type modelState struct{}\n",
		},
		{
			Tool:    "completion_gate",
			Summary: "Mutation completion gate rejected final answer",
			Error:   "this task asks for a workspace change, but no successful write tool result is present",
		},
	}

	prompt, hideReads, ok := runner.completionGateRecoveryPrompt(results)
	if !ok {
		t.Fatal("expected terminal no-write recovery prompt")
	}
	if !hideReads {
		t.Fatal("repeated no-write completion after concrete source evidence should hide reads")
	}
	for _, want := range []string{
		"multiple no-write final answers",
		"Read/search/discovery tools are hidden",
		"parser-readable write tool call",
		"internal/app/model_state.go",
		"`<<QUESTION>>:`",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("terminal no-write recovery prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, notWant := range []string{"If one edit-safety fact", "`read_file`", "`search_content`"} {
		if strings.Contains(prompt, notWant) {
			t.Fatalf("terminal no-write recovery prompt should not invite more discovery with %q:\n%s", notWant, prompt)
		}
	}
}

func TestCompletionGateRecoveryPromptForDirectRequiredPathImplementationNoWrite(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"internal/productworkflow/coverage_gate.go", "internal/productworkflow/coverage_gate_test.go"},
		},
	}

	prompt, hideReads, ok := runner.completionGateRecoveryPrompt([]workspace.ToolResult{
		{Tool: "read_file", Summary: "Read internal/productworkflow/coverage_gate.go lines 1-220 (total lines: 220)"},
		{Tool: "context_query", Summary: "Answered query for ValidatePRDTDDObligationCrossLinks"},
	})
	if !ok {
		t.Fatalf("expected completion gate recovery prompt for direct required-path implementation")
	}
	if hideReads {
		t.Fatalf("semantic required-path implementation recovery should leave bounded reads visible")
	}
	for _, want := range []string{
		"required edit path(s) remain unchanged",
		"internal/productworkflow/coverage_gate.go",
		"internal/productworkflow/coverage_gate_test.go",
		"parser-readable action",
		"not prose",
		"Hidden-only reasoning is not completion evidence",
		"edit_file",
		"write_file",
		"append_file",
		"apply_patch",
		"Prefer the next unread source-body request now",
		`"tool":"read_file"`,
		`"path":"internal/productworkflow/coverage_gate_test.go"`,
		"named_gap",
		"why_not_answering",
		"semantic/context lookup",
		"<<QUESTION>>",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("direct required-path recovery prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "submit_plan") {
		t.Fatalf("direct implementation recovery must not point at planner-phase submit_plan:\n%s", prompt)
	}
}

func TestCompletionGateRecoveryPromptUsesSearchContentEvidencePath(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}

	prompt, _, ok := runner.completionGateRecoveryPrompt([]workspace.ToolResult{
		{
			Tool:    "search_content",
			Summary: "Found 24 matching line(s).",
			Output:  "internal/app/rendering.go:50: if banner := m.errorBanner(); banner != \"\" {\ninternal/frontier/locate.go:12: banner",
		},
	})
	if !ok {
		t.Fatalf("expected completion gate recovery prompt")
	}
	for _, want := range []string{
		"Current evidence paths",
		"internal/app/rendering.go",
		`"tool":"read_file"`,
		`"path":"internal/app/rendering.go"`,
		"Do not call `context_pack` again",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("search-content recovery prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, `"tool":"context_pack"`) {
		t.Fatalf("search-content recovery should not request context_pack:\n%s", prompt)
	}
}

func TestCompletionGateRecoveryPromptFallsBackAfterContextPackFailure(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}

	prompt, _, ok := runner.completionGateRecoveryPrompt([]workspace.ToolResult{
		{Tool: "context_pack", Summary: "context_pack failed", Error: "index unavailable"},
	})
	if !ok {
		t.Fatalf("expected completion gate recovery prompt")
	}
	for _, want := range []string{
		"do not call `context_pack` again",
		"bounded fallback discovery phase",
		"`search_files`",
		"`search_content`",
		"If a literal phrase missed, broaden",
		"`read_file`",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("context fallback recovery prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, `"tool":"context_pack"`) {
		t.Fatalf("context_pack failure recovery should not ask for another context_pack call:\n%s", prompt)
	}
}

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
	}, []workspace.ToolResult{{Tool: "read_file", Summary: "Read internal/app/rendering.go", Output: "rightInfo := statusStyle.Render(...) "}})

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
		RequiredEditPaths: []string{"internal/productworkflow/coverage_gate.go", "internal/productworkflow/coverage_gate_test.go"},
	}, []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read internal/productworkflow/coverage_gate.go lines 1-220"},
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
		ReasoningContent: "I should inspect it.\n\n```tool\n{\"tool\":\"read_file\",\"path\":\"internal/app/rendering.go\",\"start_line\":1,\"end_line\":180,\"named_gap\":\"render branch\",\"why_not_answering\":\"need source\"}\n```",
	}, nil)
	if !ok {
		t.Fatalf("expected reasoning tool request to be salvaged")
	}
	if req.Tool != "read_file" || req.Path != "internal/app/rendering.go" {
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
			`{"tool":"submit_plan","rationale":"Replace the banner model path with the configured alias.","edits":[{"path":"internal/app/rendering.go","old_text":"rightInfo := statusStyle.Render(serverState + \"  \" + util.Fallback(m.modelID, \"unknown\"))","new_text":"rightInfo := statusStyle.Render(serverState + \"  \" + util.Fallback(m.currentAliasForRole(\"writer\"), \"unknown\"))","purpose":"Display the model alias in the TUI banner instead of the raw model path."}]}` +
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
		Output:  "internal/app/rendering.go:50: banner",
	}})
	if !ok {
		t.Fatalf("expected deterministic read recovery")
	}
	if req.Tool != "read_file" || req.Path != "internal/app/rendering.go" {
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
			`f33 internal/workspace/tool_schemas.go:120: "model alias"`,
			`f6 internal/app/approval.go:51:// invariant that the banner always matches a real request`,
			`f8 internal/app/rendering.go:50: if banner := m.errorBanner(); banner != "" {`,
			`f8 internal/app/rendering.go:51: footerLines = append(footerLines, errorStyle.Render(banner))`,
			`f3 internal/frontier/locate.go:169: if ContainsAny(normalized, "top", "header", "banner") {`,
		}, "\n"),
	}})
	if !ok {
		t.Fatalf("expected deterministic read recovery")
	}
	if req.Tool != "read_file" || req.Path != "internal/app/rendering.go" {
		t.Fatalf("unexpected ranked recovery read request: %#v raw=%s", req, raw)
	}
}

func TestDeterministicNoWriteRecoveryPrefersObjectiveSpecificApplyPath(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			Objective:         planExecuteStatusMigrationApplyTUIObjective,
			PermissionProfile: "guarded_workspace_write",
		},
	}
	req, raw, ok := runner.deterministicNoWriteRecoveryRead([]workspace.ToolResult{{
		Tool:    "search_content",
		Summary: "Found matching lines.",
		Output: strings.Join([]string{
			`internal/productworkflow/migration.go:34: // status migration planning metadata`,
			`cmd/idiom/product_workflow.go:65: idiom product status-migration --apply updates workflow_state, events, and index`,
			`internal/productworkflow/migration_apply.go:88: tmpPath := workflowPath + ".tmp"`,
			`internal/productworkflow/migration_apply.go:92: if err := rejectSymlinkTempPath(tmpPath); err != nil {`,
		}, "\n"),
	}})
	if !ok {
		t.Fatalf("expected deterministic read recovery")
	}
	if req.Tool != "read_file" || req.Path != "internal/productworkflow/migration_apply.go" {
		t.Fatalf("unexpected ranked recovery read request: %#v raw=%s", req, raw)
	}
}

func TestDeterministicNoWriteRecoveryDoesNotReadUnreadBetterCandidateAfterFirstRead(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			Objective:         planExecuteStatusMigrationApplyTUIObjective,
			PermissionProfile: "guarded_workspace_write",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "search_content",
			Summary: "Found matching lines.",
			Output: strings.Join([]string{
				`internal/productworkflow/migration.go:34: // status migration planning metadata`,
				`cmd/idiom/product_workflow.go:65: idiom product status-migration --apply updates workflow_state, events, and index`,
				`internal/productworkflow/migration_apply.go:88: tmpPath := workflowPath + ".tmp"`,
				`internal/productworkflow/migration_apply.go:92: if err := rejectSymlinkTempPath(tmpPath); err != nil {`,
			}, "\n"),
		},
		{
			Tool:    "read_file",
			Summary: "Read f97 internal/productworkflow/migration.go lines 1-180",
			Output:  "type StatusMigrationPlan struct{}",
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
		Output:  `{"candidate_files":[{"path":"internal/app/model_popup.go"}]}`,
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
		workspace.ToolRequest{Tool: "read_file", Path: "internal/app/model_popup.go"},
		[]workspace.ToolResult{{Tool: "context_pack", Summary: "packet", Output: `{"candidate_files":[{"path":"internal/app/model_popup.go"}]}`}},
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
		workspace.ToolRequest{Tool: "read_file", Path: "internal/app/model_popup.go"},
		[]workspace.ToolResult{{Tool: "context_pack", Summary: "packet", Output: `{"candidate_files":[{"path":"internal/app/other.go"}]}`}},
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
		{Tool: "context_pack", Summary: "packet", Output: `{"candidate_files":[{"path":"internal/app/banner.go"}]}`},
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
			Output:  `{"candidate_files":[{"path":"internal/productworkflow/coverage_gate_test.go"}]}`,
		},
		{
			Tool:    "read_file",
			Summary: "Read f1 docs/design-tdd-coverage.md lines 1-180 (total lines: 815)",
			Output:  "Phase 1: EARS And BDD Coverage Checks\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read f2 internal/productworkflow/coverage_gate.go lines 1-180 (total lines: 552)",
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
		Summary: "Read f1 internal/productworkflow/coverage_gate.go lines 1-120 (total lines: 552)",
		Output:  "func EvaluateCoverageGate() {}\n",
	}, {
		Tool:    "completion_gate",
		Summary: "Mutation completion gate rejected final answer",
		Error:   "this task asks for a workspace change, but no successful write tool result is present",
	}}

	summary, errorText, prompt, reject := runner.implementationTargetWriteReadyNonWriteRecovery(
		workspace.ToolRequest{Tool: "read_file", Path: "internal/productworkflow/coverage_gate.go"},
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
		workspace.ToolRequest{Tool: "edit_file", Path: "internal/productworkflow/coverage_gate.go"},
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
				"candidate file: internal/app/implementation_dispatch.go",
				"candidate file: internal/app/sessions_panel.go",
			},
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Redirected internal/app/implementation_dispatch.go to code_navigate",
			Output:  "Use code_navigate for source-body evidence in internal/app/implementation_dispatch.go.",
		},
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/app/sessions_panel.go lines 1-219 (total lines: 219)",
			Output:  "func renderSessionsPanelContent() {}\nfunc handleSessionsPanelKey() {}\n",
		},
	}

	if !implementationTargetWriteReadySurfaceActive(runner.RuntimeBrief, results) {
		t.Fatal("test setup should be in implementation write-ready state after plausible source-body evidence")
	}
	summary, errorText, prompt, reject := runner.implementationTargetWriteReadyNonWriteRecovery(
		workspace.ToolRequest{Tool: "code_navigate", Path: "internal/app/model_state.go"},
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
	for _, want := range []string{"Source-body evidence is already available", "internal/app/sessions_panel.go", "parser-readable write"} {
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
				"- `internal/app/app.go` (read_only_evidence): weak packet candidate",
				"- `internal/agent/phase_contract.go` (editable): matched task terms",
				"- `internal/agent/runner.go` (editable): matched task terms",
			},
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/app/app.go lines 1-55 (total lines: 55)",
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
				"route_summary: Read EvaluateCoverageGate in internal/productworkflow/coverage_gate.go lines 1-8",
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
				"route_summary: Read EvaluateCoverageGate in internal/productworkflow/coverage_gate.go lines 1-8",
				"1:func EvaluateCoverageGate() bool {",
				"2:\treturn true",
				"3:}",
			}, "\n"),
		},
	}

	if !runner.implementationTargetWriteReadySourceRefreshAllowed(
		workspace.ToolRequest{Tool: "read_file", Path: "internal/productworkflow/coverage_gate.go"},
		results,
		0,
	) {
		t.Fatal("expected one same-path source refresh to be allowed after symbol-body evidence")
	}
	if !runner.implementationTargetWriteReadySourceReadAllowed(
		workspace.ToolRequest{
			Tool:      "read_file",
			Path:      "internal/productworkflow/coverage_gate_test.go",
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
			Path: "internal/productworkflow/coverage_gate_test.go",
		},
		results,
		1,
	) {
		t.Fatal("expected rangeless new-target source read to remain available after one unrelated write-ready source expansion")
	}
	if runner.implementationTargetWriteReadySourceReadAllowed(
		workspace.ToolRequest{
			Tool:      "read_file",
			Path:      "internal/productworkflow/coverage_gate_test.go",
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
		"write":           {Tool: "edit_file", Path: "internal/productworkflow/coverage_gate.go"},
	} {
		if runner.implementationTargetWriteReadySourceReadAllowed(req, results, 0) {
			t.Fatalf("%s should not receive the source-read allowance", name)
		}
	}
	if runner.implementationTargetWriteReadySourceRefreshAllowed(
		workspace.ToolRequest{Tool: "read_file", Path: "internal/productworkflow/coverage_gate.go"},
		results,
		1,
	) {
		t.Fatal("source-refresh allowance must be capped at one use")
	}
	withFullRead := append(append([]workspace.ToolResult{}, results...), workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read internal/productworkflow/coverage_gate.go lines 1-8 (total lines: 8)",
		Output:  "1:func EvaluateCoverageGate() bool {\n2:\treturn true\n3:}\n",
	})
	if runner.implementationTargetWriteReadySourceRefreshAllowed(
		workspace.ToolRequest{Tool: "read_file", Path: "internal/productworkflow/coverage_gate.go"},
		withFullRead,
		0,
	) {
		t.Fatal("same-path source refresh should not be allowed after a full read_file result")
	}
	summary, errorText, prompt, reject := runner.implementationTargetWriteReadyNonWriteRecovery(
		workspace.ToolRequest{Tool: "read_file", Path: "internal/productworkflow/coverage_gate.go"},
		withFullRead,
	)
	if !reject {
		t.Fatal("range-less full reread after a full source read should be rejected as non-progress")
	}
	joined := summary + " " + errorText + " " + prompt
	for _, want := range []string{"target-write-ready", "internal/productworkflow/coverage_gate.go", "already available", "parser-readable write"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected full-reread recovery text to contain %q, got %q", want, joined)
		}
	}
	if _, _, _, reject := runner.implementationTargetWriteReadyNonWriteRecovery(
		workspace.ToolRequest{Tool: "code_navigate", Path: "internal/productworkflow/coverage_gate.go", Capability: "file_outline"},
		withFullRead,
	); !reject {
		t.Fatal("same-path navigation after a full source read should be rejected as non-progress")
	}
	if _, _, _, reject := runner.implementationTargetWriteReadyNonWriteRecovery(
		workspace.ToolRequest{Tool: "read_file", Path: "internal/productworkflow/coverage_gate.go", StartLine: 4, EndLine: 8},
		withFullRead,
	); !reject {
		t.Fatal("new ranged reads after a full source read should be rejected in write-ready state")
	}
}

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
			Summary: "Read f1 internal/app/sessions_panel.go lines 1-219 (total lines: 219)",
			Output:  "package app\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read f2 internal/app/sessions.go lines 1-100 (total lines: 418)",
			Output:  "package app\n",
		},
	}

	if !implementationTargetWriteReadySurfaceActive(runner.RuntimeBrief, results) {
		t.Fatal("test setup should be in write-ready after plausible session source evidence")
	}
	if !runner.implementationTargetWriteReadySourceReadAllowed(
		workspace.ToolRequest{
			Tool:      "read_file",
			Path:      "internal/app/sessions.go",
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
			Path:      "internal/app/sessions.go",
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
			Path:      "internal/app/sessions.go",
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
			Path:      "internal/app/sessions.go",
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
		Summary: "Read f3 internal/app/sessions.go lines 100-200 (total lines: 418)",
		Output:  "package app\n",
	}, workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read f4 internal/app/sessions.go lines 201-300 (total lines: 418)",
		Output:  "package app\n",
	})
	if runner.implementationTargetWriteReadySourceReadAllowed(
		workspace.ToolRequest{
			Tool:      "read_file",
			Path:      "internal/app/sessions.go",
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
			Summary: "Read f1 internal/app/sessions_panel.go lines 1-219 (total lines: 219)",
			Output:  "package app\n",
		},
		{
			Tool:    "read_file",
			Summary: "Redirected internal/app/sessions.go to code_navigate",
			Output:  "Use code_navigate for source-body evidence in internal/app/sessions.go.",
		},
	}

	if !implementationTargetWriteReadySurfaceActive(runner.RuntimeBrief, results) {
		t.Fatal("test setup should be in write-ready after plausible session source evidence")
	}
	if !runner.implementationTargetWriteReadySourceReadAllowed(
		workspace.ToolRequest{Tool: "code_navigate", Path: "internal/app/sessions.go", Capability: "read_symbol", Pattern: "SessionList"},
		results,
		implementationTargetWriteReadySourceReadAllowanceLimit,
	) {
		t.Fatal("expected exact redirected source-body follow-up to satisfy the pending source-read obligation")
	}
	if !runner.implementationTargetWriteReadySourceReadAllowed(
		workspace.ToolRequest{
			Tool:            "code_navigate",
			Path:            "internal/app/sessions.go",
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
		"missing source-body capability": {Tool: "code_navigate", Path: "internal/app/sessions.go"},
		"references":                     {Tool: "code_navigate", Path: "internal/app/sessions.go", Capability: "references", Pattern: "sessions.viewport"},
		"read symbol missing pattern":    {Tool: "code_navigate", Path: "internal/app/sessions.go", Capability: "read_symbol"},
	} {
		if runner.implementationTargetWriteReadySourceReadAllowed(req, results, implementationTargetWriteReadySourceReadAllowanceLimit) {
			t.Fatalf("%s should not satisfy the pending redirected source-body obligation", name)
		}
	}
	summary, errorText, prompt, reject := runner.implementationTargetWriteReadyNonWriteRecovery(
		workspace.ToolRequest{Tool: "code_navigate", Path: "internal/app/sessions.go", Capability: "references", Pattern: "sessions.viewport"},
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
		workspace.ToolRequest{Tool: "code_navigate", Path: "internal/app/other.go"},
		results,
		0,
	) {
		t.Fatal("redirected source-body allowance must stay scoped to the redirected path")
	}
	if runner.implementationTargetWriteReadySourceReadAllowed(
		workspace.ToolRequest{Tool: "read_file", Path: "internal/app/other.go", StartLine: 1, EndLine: 80},
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
			"route_summary: Read SessionList in internal/app/sessions.go lines 1-8",
			"1:func SessionList() []Session {",
			"2:\treturn nil",
			"3:}",
		}, "\n"),
	})
	if runner.implementationTargetWriteReadySourceReadAllowed(
		workspace.ToolRequest{Tool: "code_navigate", Path: "internal/app/sessions.go"},
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
			"route_summary: Outlined 1 top-level declaration(s) in internal/app/sessions.go.",
			"declarations:",
			"- SessionList lines 1-8",
		}, "\n"),
	})
	if runner.implementationTargetWriteReadySourceReadAllowed(
		workspace.ToolRequest{
			Tool:            "code_navigate",
			Path:            "internal/app/sessions.go",
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
		workspace.ToolRequest{Tool: "code_navigate", Path: "internal/app/sessions.go", Capability: "read_symbol", Pattern: "SessionList"},
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
			Summary: "Read f1 internal/app/sessions_panel.go lines 1-219 (total lines: 219)",
			Output:  "package app\n",
		},
	}

	if !implementationTargetWriteReadySurfaceActive(runner.RuntimeBrief, results) {
		t.Fatal("test setup should be in write-ready after plausible session source evidence")
	}
	if !runner.implementationTargetWriteReadySourceReadAllowed(
		workspace.ToolRequest{
			Tool:     "search_content",
			Path:     "internal/app",
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
		"empty search":        {Tool: "search_content", Path: "internal/app", NamedGap: "find related state"},
		"short search":        {Tool: "search_content", Path: "internal/app", Pattern: "ui", NamedGap: "find related state"},
		"missing named gap":   {Tool: "search_content", Path: "internal/app", Pattern: "type sessionsModel"},
		"broad directory":     {Tool: "search_content", Path: "internal", Pattern: "type sessionsModel", NamedGap: "find related state"},
		"repo-wide search":    {Tool: "search_content", Path: ".", Pattern: "type sessionsModel", NamedGap: "find related state"},
		"generic code search": {Tool: "search_content", Path: "internal/app", Pattern: "return", NamedGap: "find related state"},
		"all-caps search":     {Tool: "search_content", Path: "internal/app", Pattern: "TODO", NamedGap: "find related state"},
		"already sourced":     {Tool: "search_content", Path: "internal/app", Pattern: "package app", NamedGap: "find related state"},
		"search files":        {Tool: "search_files", Pattern: "sessions", NamedGap: "find related state"},
		"generic navigation":  {Tool: "code_navigate", Path: "internal/app", Capability: "file_outline"},
	} {
		if runner.implementationTargetWriteReadySourceReadAllowed(req, results, 0) {
			t.Fatalf("%s should not receive write-ready targeted localization allowance", name)
		}
	}
	if runner.implementationTargetWriteReadySourceReadAllowed(
		workspace.ToolRequest{Tool: "search_content", Path: "internal/app", Pattern: "type sessionsModel"},
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
		{Tool: "edit_file", Summary: "Edited internal/app/model_swap.go"},
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
		"internal/app/model_swap.go",
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
	if !strings.Contains(got, "whole-file/new-file shape") {
		t.Fatalf("nudge must include the new-file/whole-file submit_plan shape; got %q", got)
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
		RequiredEditPaths: []string{"internal/agent/runner.go"},
	}
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output:  "capability: read_symbol\nlanguage: go\nrouted_tool: go_read_symbol\nroute_summary: Read Runner.Run in internal/agent/runner.go lines 88-210\n\n88:func (r *Runner) Run() {}",
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
	var requests int
	var recoveryPayload requestPayload
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
	var sawTextualReadAction bool
	var sawReadEvidence bool
	var sawTextualTerminalSurface bool
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

func TestPlanExecuteTerminalTextualPruneDropsStaleScaffoldingOnly(t *testing.T) {
	messages := []llama.ChatMessage{
		{Role: "user", Content: "plan edit"},
		{Role: "system", Content: "[frontier step instruction]\n{\"current_state\":{\"tool_manifest\":[{\"tool\":\"read_file\"}]}}"},
		{Role: "system", Content: "[frontier runtime brief]\n{\"allowed_tools\":[\"read_file\"]}"},
		{Role: "system", Content: "# Textual Tool Call Protocol\n- read_file {\"tool\":\"read_file\",\"path\":\"a.go\"}"},
		{Role: "system", Content: "REQUIRED SOURCE READ PENDING: a required_edit_path has not produced source-body evidence yet.\n\nNext action MUST be exactly one visible source-body tool call for that path before `submit_plan`:\n  1. `code_navigate` with `capability:\"read_symbol\"` for the relevant declaration in a.go, or\n  2. `read_file` with explicit `start_line`/`end_line` copied from the outline/search hit."},
		{Role: "system", Content: "Note: your `read_file` call was redirected to `code_navigate` by lane routing. The rewritten call will execute and return its result; use that result, not another retry of `read_file`."},
		{Role: "system", Content: "AUTHORITATIVE_CONSTRAINT: preserve this unrelated system guidance."},
		{Role: "system", Content: "Read f1 a.go lines 1-20 (total lines: 20)\n1:package a"},
		{Role: "assistant", Content: `{"tool":"read_file","path":"a.go"}`},
		{Role: "assistant", Content: "I should now submit the plan."},
	}

	pruned, droppedActions, droppedScaffolding := prunePlanThenExecuteTerminalTextualMessages(messages)
	if droppedActions != 2 || droppedScaffolding != 5 {
		t.Fatalf("drops = actions:%d scaffolding:%d, want 2/5; messages=%#v", droppedActions, droppedScaffolding, pruned)
	}
	joined := ""
	for _, msg := range pruned {
		joined += msg.Role + "\n" + msg.Content + "\n"
	}
	if !strings.Contains(joined, "plan edit") || !strings.Contains(joined, "Read f1 a.go") {
		t.Fatalf("terminal prune must preserve user objective and source evidence, got:\n%s", joined)
	}
	if !strings.Contains(joined, "AUTHORITATIVE_CONSTRAINT") {
		t.Fatalf("terminal prune must preserve unrelated system authority, got:\n%s", joined)
	}
	for _, stale := range []string{"[frontier step instruction]", "[frontier runtime brief]", "# Textual Tool Call Protocol", "REQUIRED SOURCE READ PENDING", "redirected to `code_navigate`", `"tool":"read_file"`, "I should now submit"} {
		if strings.Contains(joined, stale) {
			t.Fatalf("terminal prune retained stale scaffolding/action %q:\n%s", stale, joined)
		}
	}
}

func TestPlanExecuteTerminalTextualCompactContextPreservesEvidenceOnly(t *testing.T) {
	messages := []llama.ChatMessage{
		{Role: "system", Content: "GLOBAL TOOL SURFACE\n- read_file {\"tool\":\"read_file\",\"path\":\"a.go\"}\n- code_navigate {\"tool\":\"code_navigate\"}"},
		{Role: "user", Content: "plan edit"},
		{Role: "system", Content: "AUTHORITATIVE_CONSTRAINT: preserve bounded write scope."},
		{Role: "system", Content: "Read f1 a.go lines 1-20 (total lines: 20)\n1:package a\n2:func Value() int { return 1 }"},
		{Role: "assistant", Content: `{"tool":"read_file","path":"a.go"}`},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.go lines 1-20 (total lines: 20)", Output: "1:package a\n2:func Value() int { return 1 }"},
		{Tool: "read_file", Summary: "Read unrelated.go lines 1-5 (total lines: 5)", Output: "1:package unrelated"},
	}

	pruned, _, _ := prunePlanThenExecuteTerminalTextualMessages(messages)
	compact, dropped, evidenceMessages := compactPlanThenExecuteTerminalTextualMessages(pruned, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.go"},
	}, results)

	if dropped == 0 {
		t.Fatalf("expected compact terminal context to drop broad prior system context, got %#v", compact)
	}
	if evidenceMessages != 1 {
		t.Fatalf("evidenceMessages = %d, want 1; compact=%#v", evidenceMessages, compact)
	}
	joined := ""
	for _, msg := range compact {
		joined += msg.Role + "\n" + msg.Content + "\n"
	}
	for _, want := range []string{
		"plan edit",
		"AUTHORITATIVE_CONSTRAINT: preserve bounded write scope.",
		planThenExecuteTerminalSourceEvidencePrefix,
		"Read f1 a.go lines 1-20",
		"2:func Value() int { return 1 }",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("compact terminal context missing %q:\n%s", want, joined)
		}
	}
	for _, stale := range []string{
		"GLOBAL TOOL SURFACE",
		`"tool":"read_file"`,
		`"tool":"code_navigate"`,
		"unrelated.go",
	} {
		if strings.Contains(joined, stale) {
			t.Fatalf("compact terminal context retained stale/unrelated content %q:\n%s", stale, joined)
		}
	}
}

func TestPlanExecuteRequiredPathReadinessNudgeMakesSubmitPlanPrimary(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.go", "b.go"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.go lines 1-20 (total lines: 20)", Output: "1:package a"},
		{Tool: "read_file", Summary: "Read f2 b.go lines 1-20 (total lines: 20)", Output: "1:package b"},
	}

	nudge := planThenExecuteReadyToSubmitNudge(brief, results)
	for _, want := range []string{
		"Next visible action MUST be exactly one parser-readable `submit_plan` request",
		"one edit entry for every required_edit_path",
		"Narrow exceptions only",
		"Use `Decision: source-satisfied` only when every required path already contains the requested final state",
		"Use `<<QUESTION>>:` only when one exact path-specific blocker prevents drafting any valid `submit_plan` edit",
		"Default to submitting `submit_plan` now",
	} {
		if !strings.Contains(nudge, want) {
			t.Fatalf("readiness nudge missing %q:\n%s", want, nudge)
		}
	}
	for _, forbidden := range []string{
		"Next action MUST be one of:",
		"State is sufficient to submit the plan or close as source-satisfied",
		"`Decision: source-satisfied` naming every required path",
	} {
		if strings.Contains(nudge, forbidden) {
			t.Fatalf("readiness nudge still presents coequal terminal menu %q:\n%s", forbidden, nudge)
		}
	}
}

func TestPlanExecuteTerminalPruneDropsOrphanableNativeToolResultHistory(t *testing.T) {
	messages := []llama.ChatMessage{
		{Role: "user", Content: "plan edit"},
		{Role: "system", Content: "Read f1 a.go lines 1-20 (total lines: 20)\n1:package a"},
		{Role: "assistant", ToolCalls: []llama.ToolCall{{
			ID:   "call_submit",
			Type: "function",
			Function: llama.ToolCallFunc{
				Name:      "submit_plan",
				Arguments: `{"edits":[]}`,
			},
		}}},
		{Role: "tool", ToolCallID: "call_submit", Name: "submit_plan", Content: "submit_plan rejected: missing required edit path(s): a.go"},
		{Role: "system", Content: "AUTHORITATIVE_CONSTRAINT: preserve this unrelated system guidance."},
	}

	pruned, droppedActions, droppedScaffolding := prunePlanThenExecuteTerminalTextualMessages(messages)
	if droppedActions != 2 || droppedScaffolding != 0 {
		t.Fatalf("drops = actions:%d scaffolding:%d, want 2/0; messages=%#v", droppedActions, droppedScaffolding, pruned)
	}
	joined := ""
	for _, msg := range pruned {
		if msg.Role == "tool" || len(msg.ToolCalls) > 0 || strings.TrimSpace(msg.ToolCallID) != "" {
			t.Fatalf("terminal prune must not leave orphanable native tool history: %#v", pruned)
		}
		joined += msg.Role + "\n" + msg.Content + "\n"
	}
	if !strings.Contains(joined, "Read f1 a.go") || !strings.Contains(joined, "AUTHORITATIVE_CONSTRAINT") {
		t.Fatalf("terminal prune should preserve source evidence and unrelated authority, got:\n%s", joined)
	}
}

func TestPlanExecuteNativeSubmitPlanRecoveryKeepsRequiredToolChoice(t *testing.T) {
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
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "auto",
	}, brief, results, true)

	if opts.ToolChoice != "required" || len(opts.Tools) != 1 || !chatOptionsExposeTool(opts, "submit_plan") {
		t.Fatalf("native submit_plan recovery should require only submit_plan, got tools=%#v choice=%q", opts.Tools, opts.ToolChoice)
	}
	if opts.EnableThinking == nil || *opts.EnableThinking {
		t.Fatalf("native submit_plan recovery should keep strict visible tool mode, got %#v", opts.EnableThinking)
	}
}

func TestPlanExecuteNativeSubmitPlanCorrectionPromptStaysNativeOnly(t *testing.T) {
	prompt := planThenExecuteNativeSubmitPlanCorrectionPrompt(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.go", "b.go"},
	}, workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected: required edit path coverage mismatch",
		Error:   "missing required edit path(s): `b.go`.",
	})

	for _, forbidden := range []string{"Decision: source-satisfied", "<<QUESTION>>", "Textual-tool backends", "visible JSON object", "textual JSON"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("native correction prompt should not advertise %q:\n%s", forbidden, prompt)
		}
	}
	for _, want := range []string{"native `submit_plan`", "a.go, b.go", "Next submit_plan coverage checklist", "b.go=missing-entry", "Do not answer in prose"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("native correction prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestPlanExecuteTextualSkeletonDefaultsExistingFilesToLineRange(t *testing.T) {
	prompt := planThenExecuteTextualSubmitPlanSkeleton([]planThenExecuteSubmitPlanSkeletonEntry{{
		Path:      "a.go",
		StartLine: 10,
		EndLine:   12,
	}})

	for _, want := range []string{"required edit path: a.go", "start_line 10", "end_line 12", "complete final replacement"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("textual skeleton missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, `"insert_after_line":`) {
		t.Fatalf("textual skeleton should not default existing paths to insert_after_line:\n%s", prompt)
	}
}

func TestPlanExecuteNativeTerminalSurfaceIncludesRequiredPathSkeleton(t *testing.T) {
	t.Skip("MLX now supports native tools; this test validates the old textual protocol path")
	runner := &Runner{
		Client: llama.NewClient("http://example.invalid", time.Second).WithBackend(config.BackendMLX),
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
	messages := []llama.ChatMessage{{Role: "user", Content: "plan edit"}}

	runner.refreshPlanThenExecuteTerminalToolSurface(&messages, results, "test")
	joined := planExecuteJoinedMessages(messages)

	if strings.Count(joined, planThenExecuteCurrentSubmitPlanSkeletonPrefix) != 1 {
		t.Fatalf("textual terminal surface should include exactly one current skeleton:\n%s", joined)
	}
	for _, want := range []string{
		"# Terminal Plan-Execute Textual Tool Surface",
		"submit_plan_called",
		"source_satisfied_or_blocker_only_with_per_path_evidence",
		"Parser-readable submit_plan contract",
		"required edit path: a.go",
		"required edit path: b.go",
		"evidence: Read a.go lines 1-20",
		"insert_after_line",
		"old_text",
		"start_line",
		"start_line 1",
		"end_line 20",
		"`insert_after_line` targets an existing 1-based line in an existing file",
		"The shown source ranges are evidence handles, not proposed edits",
		"Line-range `content` replaces every current source line",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("textual terminal surface missing %q:\n%s", want, joined)
		}
	}
	for _, forbidden := range []string{"# Terminal Plan-Execute Native Tool Surface", "Native `submit_plan` tool-argument shape"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("textual terminal surface should not ask for %q:\n%s", forbidden, joined)
		}
	}
	for _, forbidden := range []string{"submit_plan_called_or_source_satisfied", "edits_or_source_satisfied_evidence_listed_in_summary"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("textual terminal surface should not present source-satisfied as a coequal final critic %q:\n%s", forbidden, joined)
		}
	}
}

func TestPlanExecuteTerminalSurfaceRefreshAllowsSelectedContextPackCandidate(t *testing.T) {
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
	runner := &Runner{RuntimeBrief: brief}
	messages := []llama.ChatMessage{{Role: "user", Content: "plan feature edit"}}

	runner.refreshPlanThenExecuteTerminalToolSurface(&messages, results, "context_pack_candidate")
	joined := planExecuteJoinedMessages(messages)

	if !strings.Contains(joined, "# Terminal Plan-Execute Textual Tool Surface") &&
		!strings.Contains(joined, "# Terminal Plan-Execute Native Tool Surface") {
		t.Fatalf("selected context-pack candidate should render terminal tool surface:\n%s", joined)
	}
	if !strings.Contains(joined, "required edit path: pkg/feature.go") && !strings.Contains(joined, `path: "pkg/feature.go"`) {
		t.Fatalf("selected context-pack candidate should appear in submit skeleton:\n%s", joined)
	}
	if strings.Contains(joined, "No concrete selected edit path") {
		t.Fatalf("selected context-pack candidate should not render no-path blocker skeleton:\n%s", joined)
	}
}

func TestPlanExecuteTerminalSurfaceRefreshSkipsUnreadContextCandidate(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote .idiom/comprehension/views/test.json with 2 candidate file(s)",
			Output:  `{"candidate_files":[{"path":"pkg/feature.go"},{"path":"pkg/feature_test.go"}]}`,
		},
		{
			Tool:    "read_file",
			Summary: "Read f1 pkg/feature.go lines 1-4 (total lines: 4)",
			Output:  "1:package pkg\n2:\n3:func Hardened() bool { return true }\n",
		},
	}
	runner := &Runner{RuntimeBrief: brief}
	messages := []llama.ChatMessage{{Role: "user", Content: "plan feature edit"}}

	runner.refreshPlanThenExecuteTerminalToolSurface(&messages, results, "context_pack_candidate")
	joined := planExecuteJoinedMessages(messages)

	if strings.Contains(joined, "# Terminal Plan-Execute Textual Tool Surface") ||
		strings.Contains(joined, "# Terminal Plan-Execute Native Tool Surface") {
		t.Fatalf("unread context-pack candidate should not render terminal tool surface:\n%s", joined)
	}
}

func TestPlanExecuteTrueNativeTerminalSurfaceIncludesRequiredPathSkeleton(t *testing.T) {
	runner := &Runner{
		Client: llama.NewClient("http://example.invalid", time.Second).WithBackend(config.BackendLlama),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go"},
		},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read a.go lines 10-40 (total lines: 80)", Output: "10:func value() int {"},
	}
	messages := []llama.ChatMessage{{Role: "user", Content: "plan edit"}}

	runner.refreshPlanThenExecuteTerminalToolSurface(&messages, results, "test")
	joined := planExecuteJoinedMessages(messages)

	if !strings.Contains(joined, "# Terminal Plan-Execute Native Tool Surface") ||
		!strings.Contains(joined, "Native `submit_plan` tool-argument shape") ||
		!strings.Contains(joined, `path: "a.go"`) ||
		!strings.Contains(joined, "line_range_default: start_line=10 + end_line=40 + content") ||
		!strings.Contains(joined, "insertion_small_only: insert_after_line + small inline content") ||
		!strings.Contains(joined, "large additions inside existing files") {
		t.Fatalf("true native terminal surface should include native skeleton:\n%s", joined)
	}
	for _, forbidden := range []string{"visible JSON object", "parser-readable JSON", "Decision: source-satisfied", "<<QUESTION>>"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("true native terminal surface should not advertise %q:\n%s", forbidden, joined)
		}
	}
}

func TestPlanExecuteTextualFallbackSurfaceIncludesConcreteSubmitPlanJSONSkeleton(t *testing.T) {
	runner := &Runner{
		Client: llama.NewClient("http://example.invalid", time.Second).WithBackend(config.BackendMLX),
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
	messages := []llama.ChatMessage{{Role: "user", Content: "plan edit"}}

	runner.refreshPlanThenExecuteTerminalTextualFallbackSurface(&messages, results, "test")
	joined := planExecuteJoinedMessages(messages)

	if strings.Count(joined, planThenExecuteCurrentSubmitPlanSkeletonPrefix) != 1 {
		t.Fatalf("textual fallback should include exactly one current skeleton:\n%s", joined)
	}
	for _, want := range []string{
		"# Terminal Plan-Execute Textual Tool Surface",
		"Parser-readable submit_plan contract",
		"top-level `tool:\"submit_plan\"`",
		"required edit path: a.go",
		"required edit path: b.go",
		"evidence: Read a.go lines 1-20",
		"start_line 1",
		"end_line 20",
		"complete final replacement",
		"Edit shape choices",
		"use `insert_after_line` + small inline `content` only for small insertions/appends after an existing line",
		"large additions anchored to existing lines",
		"`replace_all:true` + empty `old_text` + full `new_text`",
		"`insert_after_line` targets an existing 1-based line in an existing file",
		"The shown source ranges are evidence handles, not proposed edits",
		"Line-range `content` replaces every current source line",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("textual fallback surface missing %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, `"insert_after_line":20`) {
		t.Fatalf("textual fallback surface should not default existing paths to insert_after_line:\n%s", joined)
	}
}

func TestPlanExecuteTextualFallbackSurfaceUsesNewFileShapeForMissingPath(t *testing.T) {
	runner := &Runner{
		Client: llama.NewClient("http://example.invalid", time.Second).WithBackend(config.BackendMLX),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go", "new_file.go"},
		},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read a.go lines 1-20 (total lines: 20)", Output: "1:package a"},
		{
			Tool:    "read_file",
			Summary: "read_file failed for new_file.go",
			Error:   `path "new_file.go" is in your required_edit_paths but does not exist on disk yet - this is a NEW FILE you must create.`,
		},
	}
	messages := []llama.ChatMessage{{Role: "user", Content: "plan edit"}}

	runner.refreshPlanThenExecuteTerminalTextualFallbackSurface(&messages, results, "test")
	joined := planExecuteJoinedMessages(messages)

	for _, want := range []string{
		"required edit path: new_file.go",
		"replace_all true",
		"empty old_text",
		"complete final new_text file body",
		"For missing/new files, use only `replace_all:true`",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing-file skeleton missing %q:\n%s", want, joined)
		}
	}
	newFileStart := strings.Index(joined, "required edit path: new_file.go")
	if newFileStart < 0 {
		t.Fatalf("missing-file contract should name new file path:\n%s", joined)
	}
	if strings.Contains(joined[newFileStart:], "default source range evidence") {
		t.Fatalf("missing-file contract should not use line-range shape:\n%s", joined)
	}
}

func TestPlanExecuteNativeSurfaceUsesNewFileShapeForMissingPath(t *testing.T) {
	runner := &Runner{
		Client: llama.NewClient("http://example.invalid", time.Second).WithBackend(config.BackendLlama),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go", "new_file.go"},
		},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read a.go lines 1-20 (total lines: 20)", Output: "1:package a"},
		{
			Tool:    "read_file",
			Summary: "read_file failed for new_file.go",
			Error:   `path "new_file.go" is in your required_edit_paths but does not exist on disk yet - this is a NEW FILE you must create.`,
		},
	}
	messages := []llama.ChatMessage{{Role: "user", Content: "plan edit"}}

	runner.refreshPlanThenExecuteTerminalToolSurface(&messages, results, "test")
	joined := planExecuteJoinedMessages(messages)
	newFileStart := strings.Index(joined, `path: "new_file.go"`)
	if newFileStart < 0 {
		t.Fatalf("native missing-file skeleton should list new path:\n%s", joined)
	}
	newFileBlock := joined[newFileStart:]
	if end := strings.Index(newFileBlock, "\n\nEdit shape semantics:"); end >= 0 {
		newFileBlock = newFileBlock[:end]
	}
	if !strings.Contains(newFileBlock, `whole_or_new_file: replace_all=true + old_text="" + new_text`) {
		t.Fatalf("native missing-file skeleton should use new-file shape:\n%s", joined)
	}
	if strings.Contains(newFileBlock, "substring: old_text + new_text") ||
		strings.Contains(newFileBlock, "line_range:") {
		t.Fatalf("native missing-file skeleton should not advertise invalid anchor shapes:\n%s", joined)
	}
}

func TestPlanExecuteTerminalSkeletonCapsEvidenceWhileListingAllRequiredPaths(t *testing.T) {
	paths := make([]string, 0, planThenExecuteSubmitPlanSkeletonMaxEvidenceLines+4)
	results := make([]workspace.ToolResult, 0, planThenExecuteSubmitPlanSkeletonMaxEvidenceLines+4)
	for i := 0; i < planThenExecuteSubmitPlanSkeletonMaxEvidenceLines+4; i++ {
		path := fmt.Sprintf("file%d.go", i)
		paths = append(paths, path)
		results = append(results, workspace.ToolResult{
			Tool:    "read_file",
			Summary: fmt.Sprintf("Read %s lines 1-10 (total lines: 10)", path),
			Output:  "1:package p",
		})
	}

	skeleton := planThenExecuteTerminalSubmitPlanSkeleton(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: paths,
	}, results, true)

	for _, path := range paths {
		if !strings.Contains(skeleton, strconv.Quote(path)) {
			t.Fatalf("skeleton must still list required path %s:\n%s", path, skeleton)
		}
	}
	if got := strings.Count(skeleton, "  evidence: "); got != planThenExecuteSubmitPlanSkeletonMaxEvidenceLines {
		t.Fatalf("evidence details should be globally capped, got %d:\n%s", got, skeleton)
	}
	if !strings.Contains(skeleton, "Additional evidence handles were omitted after the template evidence cap") {
		t.Fatalf("capped skeleton should disclose omitted evidence handles:\n%s", skeleton)
	}
}

func TestPlanExecutePostRejectionSurfaceKeepsOneSkeletonAndRejectedDraftEvidence(t *testing.T) {
	t.Skip("MLX now supports native tools; this test validates the old textual protocol path")
	runner := &Runner{
		Client: llama.NewClient("http://example.invalid", time.Second).WithBackend(config.BackendMLX),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go", "b.go"},
		},
	}
	req := workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{Path: "a.go", StartLine: 1, EndLine: 3, Content: "package a\n\nfunc Value() int { return 2 }\n", Purpose: "change returned value"},
			{Path: "b.go", OldText: "return 1", NewText: "return 2", Purpose: "change returned value"},
		},
	}
	result := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected: required edit path coverage mismatch",
		Error:   "invalid edit anchor(s): `b.go: old_text not found in current source`.",
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read a.go lines 1-20 (total lines: 20)", Output: "1:package a"},
		{Tool: "read_file", Summary: "Read b.go lines 1-20 (total lines: 20)", Output: "1:package b"},
		result,
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "route_summary: Read Target in b.go lines 1-20\n1:package b"},
	}
	messages := []llama.ChatMessage{
		{Role: "system", Content: "# Terminal Plan-Execute Native Tool Surface\n" + planThenExecuteNativeSubmitPlanSkeleton([]planThenExecuteSubmitPlanSkeletonEntry{{Path: "stale.go"}})},
		{Role: "system", Content: planThenExecuteRejectedSubmitPlanDraftEvidencePrefix + "\nstale draft"},
	}

	replacePlanThenExecuteRejectedSubmitPlanDraftEvidence(&messages, req, result)
	runner.refreshPlanThenExecuteTerminalToolSurface(&messages, results, "submit_plan_rejected")
	joined := planExecuteJoinedMessages(messages)

	if strings.Count(joined, planThenExecuteCurrentSubmitPlanSkeletonPrefix) != 1 {
		t.Fatalf("post-rejection surface should have exactly one current skeleton:\n%s", joined)
	}
	if strings.Count(joined, planThenExecuteRejectedSubmitPlanDraftEvidencePrefix) != 1 {
		t.Fatalf("post-rejection surface should preserve exactly one rejected draft evidence block:\n%s", joined)
	}
	for _, want := range []string{"required edit path: b.go", "path=a.go lines=1-3", "return 2", "old_text not found"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("post-rejection surface missing %q:\n%s", want, joined)
		}
	}
	if !strings.Contains(joined, "required edit path: a.go") {
		t.Fatalf("post-rejection correction skeleton must preserve required-path coverage:\n%s", joined)
	}
	if strings.Contains(joined, "stale.go") || strings.Contains(joined, "stale draft") {
		t.Fatalf("post-rejection surface retained stale skeleton/evidence:\n%s", joined)
	}
}

func planExecuteJoinedMessages(messages []llama.ChatMessage) string {
	var b strings.Builder
	for _, msg := range messages {
		b.WriteString(msg.Role)
		b.WriteByte('\n')
		b.WriteString(msg.Content)
		b.WriteByte('\n')
	}
	return b.String()
}

func TestPlanExecuteRejectedSubmitPlanDraftEvidencePreservesFailingBody(t *testing.T) {
	result := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected",
		Error:   "edits[0]: preflight syntax validation failed for a.go at line 4: ./a.go:4:2: expected declaration, found '}'",
	}
	prompt := planThenExecuteRejectedSubmitPlanDraftEvidence(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{
				Path:      "a.go",
				StartLine: 1,
				EndLine:   5,
				Content:   "package a\n\nfunc Value() int {\n\treturn 2\n}\n}\n",
				Purpose:   "change returned value",
			},
			{
				Path:    "b.go",
				OldText: "return 1",
				NewText: "return 2",
				Purpose: "follow-up edit",
			},
		},
	}, result)
	for _, want := range []string{
		planThenExecuteRejectedSubmitPlanDraftEvidencePrefix,
		"path=a.go",
		"status=invalid_anchor_or_syntax",
		"rejected_body_excerpt_not_source_evidence",
		"not source evidence",
		"func Value() int",
		"path=b.go",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("rejected draft evidence missing %q:\n%s", want, prompt)
		}
	}
	messages := []llama.ChatMessage{
		{Role: "system", Content: prompt},
		{Role: "system", Content: "# Terminal Plan-Execute Native Tool Surface\nstale"},
		{Role: "assistant", ToolCalls: []llama.ToolCall{{ID: "call_1", Type: "function", Function: llama.ToolCallFunc{Name: "submit_plan", Arguments: "{}"}}}},
		{Role: "tool", ToolCallID: "call_1", Name: "submit_plan", Content: "submit_plan rejected"},
	}
	pruned, droppedActions, droppedScaffolding := prunePlanThenExecuteTerminalTextualMessages(messages)
	if droppedActions != 2 || droppedScaffolding != 1 {
		t.Fatalf("drops = actions:%d scaffolding:%d, want 2/1", droppedActions, droppedScaffolding)
	}
	if len(pruned) != 1 || !strings.Contains(pruned[0].Content, "func Value() int") {
		t.Fatalf("terminal prune should preserve rejected draft evidence, got %#v", pruned)
	}
}

func TestPlanExecuteSubmitPlanRejectionBriefPreservesTailGuidance(t *testing.T) {
	longPrefix := strings.Repeat("planned content around syntax error ", 30)
	brief := lastSubmitPlanRejectionBrief(workspace.ToolResult{
		Tool: "submit_plan",
		Error: longPrefix + "Line-range edit detail: submit_plan edit for a.go declared start_line=10 and end_line=20. " +
			"The `content` replaces exactly that inclusive current-source range; if the replacement includes source that currently lives after end_line, extend end_line to cover it.",
	})
	for _, want := range []string{"Last submit_plan rejection", "Line-range edit detail", "extend end_line"} {
		if !strings.Contains(brief, want) {
			t.Fatalf("brief should preserve actionable tail guidance %q:\n%s", want, brief)
		}
	}
	if len(brief) > 820 {
		t.Fatalf("brief should remain bounded, got %d chars:\n%s", len(brief), brief)
	}
}

func TestPlanExecuteNativeNoToolCallFallbackSwitchesToTextualSubmitPlan(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.go", "b.go"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.go lines 1-20 (total lines: 20)"},
		{Tool: "read_file", Summary: "Read f2 b.go lines 1-20 (total lines: 20)"},
	}
	runner := &Runner{
		Client:       llama.NewClient("http://127.0.0.1:1", time.Second).WithBackend(config.BackendMLX),
		RuntimeBrief: brief,
	}
	prompt, ok := runner.planThenExecuteNativeSubmitPlanNoCallFallbackPrompt(
		errors.New(`chat request failed: {"detail":"tool_choice='required' was set but the model did not produce any tool calls."}`),
		llama.ChatOptions{ForceNativeTools: true, ToolChoice: "required"},
		results,
		0,
	)
	if !ok {
		t.Fatalf("expected native required no-tool-call error to activate textual fallback")
	}
	for _, want := range []string{"textual tool parser is active", "Emit exactly one visible JSON object", "a.go, b.go"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("fallback prompt missing %q:\n%s", want, prompt)
		}
	}
	opts := planThenExecuteTextualSubmitPlanFallbackChatOptions(llama.ChatOptions{
		ForceNativeTools: true,
		Tools:            []llama.Tool{{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}}},
		ToolChoice:       "required",
	}, brief, results)
	if opts.ForceNativeTools || len(opts.Tools) != 0 || opts.ToolChoice != "" {
		t.Fatalf("textual fallback should disable native tools, got force=%v tools=%#v choice=%q", opts.ForceNativeTools, opts.Tools, opts.ToolChoice)
	}
	if opts.EnableThinking == nil || *opts.EnableThinking {
		t.Fatalf("textual fallback should force visible output, got %#v", opts.EnableThinking)
	}
}

func TestPlanExecuteNativeNoToolCallFallbackStaysTextualAfterGateRecovery(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n\nfunc Value() int { return 1 }\n"), 0o644); err != nil {
		t.Fatalf("write a.go: %v", err)
	}
	type requestPayload struct {
		Tools      []planExecuteRequestToolPayload `json:"tools"`
		ToolChoice string                          `json:"tool_choice"`
	}
	var requests int
	var fallbackPayload requestPayload
	var postGatePayload requestPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload requestPayload
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("decode request %d: %v\n%s", requests, err, string(body))
		}
		switch requests {
		case 0:
			requests++
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": `{"tool":"read_file","path":"a.go"}`}}}})
		case 1:
			requests++
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{
				"message": map[string]any{"role": "assistant", "tool_calls": []map[string]any{{
					"id": "call_submit", "type": "function",
					"function": map[string]any{"name": "submit_plan", "arguments": `{"rationale":"incomplete","edits":[]}`},
				}}},
				"finish_reason": "tool_calls",
			}}})
		case 2:
			requests++
			http.Error(w, `{"detail":"tool_choice='required' was set but the model did not produce any tool calls."}`, http.StatusBadRequest)
		case 3:
			fallbackPayload = payload
			requests++
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": "I will submit the corrected plan now."}}}})
		case 4:
			postGatePayload = payload
			requests++
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": `{"tool":"submit_plan","rationale":"Update value.","edits":[{"path":"a.go","old_text":"return 1","new_text":"return 2","purpose":"change returned value"}]}`}}}})
		default:
			t.Fatalf("unexpected request %d", requests)
		}
	}))
	defer server.Close()

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second).WithBackend(config.BackendMLX),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 6},
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
	if len(fallbackPayload.Tools) != 0 || fallbackPayload.ToolChoice != "" {
		t.Fatalf("native no-call fallback request should be textual, got tools=%#v choice=%q", fallbackPayload.Tools, fallbackPayload.ToolChoice)
	}
	if len(postGatePayload.Tools) != 0 || postGatePayload.ToolChoice != "" {
		t.Fatalf("post-gate recovery after textual fallback must stay textual, got tools=%#v choice=%q", postGatePayload.Tools, postGatePayload.ToolChoice)
	}
	if !strings.Contains(answer, "Plan submitted with 1 edit") {
		t.Fatalf("expected submit_plan success, got:\n%s", answer)
	}
}

func TestPlanExecuteMLXSubmitPlanAfterRefreshPromptMatchesNativeTransport(t *testing.T) {
	t.Skip("MLX now supports native tools; this test validates the old textual protocol path")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n\nfunc Value() int {\n\treturn 1\n}\n"), 0o644); err != nil {
		t.Fatalf("write a.go: %v", err)
	}
	type requestPayload struct {
		Messages   []llama.ChatMessage             `json:"messages"`
		Tools      []planExecuteRequestToolPayload `json:"tools"`
		ToolChoice string                          `json:"tool_choice"`
	}
	var requests int
	var correctedSubmitPayload requestPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload requestPayload
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("decode request %d: %v\n%s", requests, err, string(body))
		}
		switch requests {
		case 0:
			requests++
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": `{"tool":"read_file","path":"a.go","start_line":1,"end_line":6}`}}}})
		case 1:
			requests++
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{
				"message": map[string]any{"role": "assistant", "tool_calls": []map[string]any{{
					"id": "call_bad_submit", "type": "function",
					"function": map[string]any{"name": "submit_plan", "arguments": `{"rationale":"Bad anchor should require refresh.","edits":[{"path":"a.go","old_text":"return missing","new_text":"return 2","purpose":"change returned value"}]}`},
				}}},
				"finish_reason": "tool_calls",
			}}})
		case 2:
			requests++
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": `{"tool":"read_file","path":"a.go","start_line":1,"end_line":6}`}}}})
		case 3:
			correctedSubmitPayload = payload
			requests++
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": `{"tool":"submit_plan","rationale":"Corrected after refresh.","edits":[{"path":"a.go","old_text":"return 1","new_text":"return 2","purpose":"change returned value"}]}`}}}})
		default:
			t.Fatalf("unexpected request %d", requests)
		}
	}))
	defer server.Close()

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second).WithBackend(config.BackendMLX),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 6},
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
			{Type: "function", Function: llama.ToolFunction{Name: "code_navigate"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "auto",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if correctedSubmitPayload.ToolChoice != "" || len(correctedSubmitPayload.Tools) != 0 {
		t.Fatalf("post-refresh MLX request should stay textual, got tools=%#v choice=%q", correctedSubmitPayload.Tools, correctedSubmitPayload.ToolChoice)
	}
	var promptText strings.Builder
	for _, msg := range correctedSubmitPayload.Messages {
		promptText.WriteString(msg.Content)
		promptText.WriteByte('\n')
	}
	joined := promptText.String()
	for _, forbidden := range []string{"native `submit_plan`", "Native `submit_plan`"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("post-refresh textual request leaked native guidance %q:\n%s", forbidden, joined)
		}
	}
	for _, want := range []string{"Textual-tool backends must emit exactly one visible JSON object", "no prose, markdown commentary"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("post-refresh textual request missing %q:\n%s", want, joined)
		}
	}
	if !strings.Contains(answer, "Plan submitted with 1 edit") {
		t.Fatalf("expected submit_plan success, got:\n%s", answer)
	}
}

func TestPlanThenExecuteUnreadRequiredSourceNudgeFiresAfterOutlineOnlyPath(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go"},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n\n1515:func runProductTDD() {}\n",
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed file_outline via go_file_outline",
			Output:  "route_summary: Outlined 33 top-level declaration(s) in cmd/idiom/product_workflow_test.go.\n",
		},
	}

	got := planThenExecuteUnreadRequiredSourceNudge(brief, results)
	if got == "" {
		t.Fatalf("expected unread required source nudge")
	}
	for _, want := range []string{
		"cmd/idiom/product_workflow_test.go",
		"read_symbol",
		"start_line",
		"Do not call `submit_plan`",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("nudge missing %q:\n%s", want, got)
		}
	}
}

func TestPlanThenExecuteUnreadRequiredSourceChatOptionsCapsNextCall(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.go", "b.go"},
	}
	results := []workspace.ToolResult{
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "route_summary: Read A in a.go lines 1-10\n\n1:func A() {}\n"},
		{Tool: "code_navigate", Summary: "Code navigation routed file_outline via go_file_outline", Output: "route_summary: Outlined 2 top-level declaration(s) in b.go.\n"},
	}
	thinkingOn := true
	opts := planThenExecuteUnreadRequiredSourceChatOptions(llama.ChatOptions{
		EnableThinking:   &thinkingOn,
		PreserveThinking: true,
		MaxTokens:        16384,
	}, brief, results)

	if opts.EnableThinking == nil || *opts.EnableThinking {
		t.Fatalf("unread-source nudge should force thinking off for the next short tool call")
	}
	if opts.PreserveThinking {
		t.Fatalf("unread-source nudge should not preserve prior hidden reasoning")
	}
	if opts.MaxTokens != 768 {
		t.Fatalf("MaxTokens = %d, want 768", opts.MaxTokens)
	}
}

func TestPlanThenExecuteReadyToSubmitNudgeWaitsForFullCoverage(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.txt", "b.txt", "c.txt"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.txt lines 1-10"},
		{Tool: "read_file", Summary: "Read f2 b.txt lines 1-10"},
		// c.txt has not been read — the nudge must wait.
	}
	if got := planThenExecuteReadyToSubmitNudge(brief, results); got != "" {
		t.Fatalf("nudge must NOT fire while a required path is unread; got %q", got)
	}
}

func TestPlanThenExecuteReadyToSubmitNudgeFiresForCandidateScopeRead(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote .idiom/comprehension/views/test.json with 1 candidate file(s)",
			Output:  `{"candidate_files":[{"path":"internal/app/rendering.go"}]}`,
		},
		{Tool: "read_file", Summary: "Read f1 internal/app/rendering.go lines 1-120"},
	}

	got := planThenExecuteReadyToSubmitNudge(brief, results)
	if got == "" {
		t.Fatalf("expected candidate-scope readiness nudge after a read")
	}
	for _, want := range []string{
		"at least one selected candidate edit path has source evidence",
		"internal/app/rendering.go",
		"submit_plan",
		"Decision: source-satisfied",
		"<<QUESTION>>",
		"Hidden reasoning is not executable evidence",
		"Allowed/candidate paths are not coverage obligations",
		"Do not reread a candidate path that already has source-body evidence",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("candidate-scope nudge missing %q: %q", want, got)
		}
	}
	if strings.Contains(got, "Only read/search again") {
		t.Fatalf("candidate-scope nudge must not retain stale optional-read language: %q", got)
	}
}

func TestPlanThenExecuteReadyToSubmitNudgeDoesNotRequireUnreadUnscopedCandidates(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote .idiom/comprehension/views/test.json with 2 candidate file(s)",
			Output:  `{"candidate_files":[{"path":"internal/productworkflow/coverage_gate.go"},{"path":"internal/productworkflow/coverage_gate_test.go"}]}`,
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read EvaluateCoverage in internal/productworkflow/coverage_gate.go lines 20-80\n\n20:func EvaluateCoverage() {}\n",
		},
	}

	got := planThenExecuteReadyToSubmitNudge(brief, results)
	if got == "" {
		t.Fatal("unscoped context candidates should receive submit-ready guidance after a selected source-body read")
	}
	for _, want := range []string{
		"coverage_gate.go",
		"Allowed/candidate paths are not coverage obligations",
		"One bounded source-read/search for a different candidate path",
		"submit_plan",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("candidate readiness nudge missing %q:\n%s", want, got)
		}
	}
}

func TestPlanThenExecuteReadyToSubmitNudgeIgnoresUnselectedEvidenceOnlyRead(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 internal/app/rendering.go lines 1-120"},
	}

	if got := planThenExecuteReadyToSubmitNudge(brief, results); got != "" {
		t.Fatalf("evidence-only unscoped reads must not receive submit-ready nudge without selected candidate path, got %q", got)
	}
}

func TestPlanThenExecuteReadyToSubmitNudgeDoesNotRequireAllAllowedCandidates(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		AllowedEditPaths:  []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go", "internal/productworkflow/coverage_gate.go"},
		RequiredEditPaths: nil,
	}
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output:  "route_summary: Read validTestImplementationLedgerJSONL in cmd/idiom/product_workflow_test.go lines 3636-3678\n\n3636:func validTestImplementationLedgerJSONL() string {\n3637:\treturn `{}`\n3638:}\n",
	}}

	got := planThenExecuteReadyToSubmitNudge(brief, results)
	if got == "" {
		t.Fatal("candidate-only allowed scope must become submit-ready after one anchor-ready candidate read")
	}
	if !strings.Contains(got, "cmd/idiom/product_workflow_test.go") || !strings.Contains(got, "Allowed/candidate paths are not coverage obligations") {
		t.Fatalf("nudge should cite the read path and not promote allowed paths to required coverage:\n%s", got)
	}
	if strings.Contains(got, "cmd/idiom/product_workflow.go") || strings.Contains(got, "internal/productworkflow/coverage_gate.go") {
		t.Fatalf("nudge should not demand unread allowed candidates as coverage obligations:\n%s", got)
	}
}

func TestPlanExecutePlanningStateKeepsCandidatesOutOfRequiredCoverage(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:       PlanThenExecutePlannerTaskFamily,
		AllowedEditPaths: []string{"pkg/feature.go", "pkg/feature_test.go"},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 pkg/feature.go lines 1-40 (total lines: 40)",
		Output:  "1:package pkg\n",
	}}

	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode != PlanExecutePlanningModeAllowedCandidate {
		t.Fatalf("mode = %q, want allowed_candidate", state.Mode)
	}
	if len(state.RequiredCoveragePaths) != 0 {
		t.Fatalf("allowed candidates must not become required coverage: %#v", state.RequiredCoveragePaths)
	}
	if !slices.Equal(state.SelectedEditPaths, []string{"pkg/feature.go"}) {
		t.Fatalf("selected edit paths should come from source evidence, got %#v", state.SelectedEditPaths)
	}
	if !slices.Contains(state.TerminalOptions, "submit_plan") {
		t.Fatalf("terminal options should include submit_plan once selected source evidence exists: %#v", state.TerminalOptions)
	}
}

func TestPlanExecutePlanningStateDoesNotPromoteEvidenceOnlyPaths(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/productworkflow/types.go lines 1-40 (total lines: 40)",
		Output:  "1:package productworkflow\n",
	}}

	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if len(state.RequiredCoveragePaths) != 0 {
		t.Fatalf("unexpected required coverage paths: %#v", state.RequiredCoveragePaths)
	}
	if len(state.SelectedEditPaths) != 0 {
		t.Fatalf("evidence-only paths must not become selected edit paths: %#v", state.SelectedEditPaths)
	}
	if slices.Contains(state.TerminalOptions, "submit_plan") {
		t.Fatalf("submit_plan must not be advertised without concrete selected edit paths: %#v", state.TerminalOptions)
	}
}

func TestPlanExecutePlanningStateDoesNotPromoteEvidenceOnlyPathViaContextPack(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "context pack",
			Output:  `{"candidate_files":[{"path":"pkg/feature.go"}]}`,
		},
		{
			Tool:    "read_file",
			Summary: "Read f1 pkg/other.go lines 1-40 (total lines: 40)",
			Output:  "1:package pkg\n",
		},
	}

	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if len(state.SelectedEditPaths) != 0 {
		t.Fatalf("context-pack candidates must not make unrelated evidence-only paths selected: %#v", state.SelectedEditPaths)
	}
	if slices.Contains(state.TerminalOptions, "submit_plan") {
		t.Fatalf("submit_plan must not be advertised without a selected edit path: %#v", state.TerminalOptions)
	}
	skeleton := planThenExecuteTerminalSubmitPlanSkeleton(brief, results, false)
	if strings.Contains(skeleton, "pkg/feature.go") || strings.Contains(skeleton, "pkg/other.go") {
		t.Fatalf("skeleton must not promote context-pack or evidence-only paths:\n%s", skeleton)
	}
	if !strings.Contains(skeleton, "No concrete selected edit path") {
		t.Fatalf("skeleton should report no safe selected path:\n%s", skeleton)
	}
}

func TestPlanExecutePlanningStateSelectsContextPackCandidateWithSourceEvidence(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "context pack",
			Output:  `{"candidate_files":[{"path":"pkg/feature.go"}]}`,
		},
		{
			Tool:    "read_file",
			Summary: "Read f1 pkg/feature.go lines 1-40 (total lines: 40)",
			Output:  "1:package pkg\n",
		},
	}

	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if !slices.Equal(state.SelectedEditPaths, []string{"pkg/feature.go"}) {
		t.Fatalf("selected edit paths should be context-pack candidates with source evidence, got %#v", state.SelectedEditPaths)
	}
	if !slices.Contains(state.TerminalOptions, "submit_plan") {
		t.Fatalf("submit_plan should be available once a typed candidate has source evidence: %#v", state.TerminalOptions)
	}
	if !planThenExecuteTerminalSubmitPlanAllowed(brief, results) {
		t.Fatal("typed context-pack candidate with source evidence should allow terminal submit_plan surface")
	}
}

func TestPlanExecutePlanningStateCorrectionSelectsInvalidArtifactPaths(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.go", "b.go"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read a.go lines 1-20 (total lines: 20)", Output: "1:package a"},
		{Tool: "read_file", Summary: "Read b.go lines 1-20 (total lines: 20)", Output: "1:package b"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "invalid edit anchor(s): `b.go: old_text not found in current source`.",
		},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "route_summary: Read Target in b.go lines 1-20\n1:package b"},
	}

	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode != PlanExecutePlanningModeSubmitCorrection {
		t.Fatalf("mode = %q, want submit_correction", state.Mode)
	}
	if !slices.Equal(state.RequiredCoveragePaths, []string{"a.go", "b.go"}) {
		t.Fatalf("required coverage paths changed unexpectedly: %#v", state.RequiredCoveragePaths)
	}
	if !slices.Equal(state.LastInvalidPaths, []string{"b.go"}) {
		t.Fatalf("last invalid paths = %#v, want b.go", state.LastInvalidPaths)
	}
	if !slices.Equal(state.SelectedEditPaths, []string{"a.go", "b.go"}) {
		t.Fatalf("correction selected edit paths must preserve required coverage, got %#v", state.SelectedEditPaths)
	}
	if !slices.Equal(planThenExecuteSubmitPlanSkeletonPaths(state), []string{"a.go", "b.go"}) {
		t.Fatalf("correction skeleton paths must preserve required coverage, got %#v", planThenExecuteSubmitPlanSkeletonPaths(state))
	}
	skeleton := planThenExecuteTerminalSubmitPlanSkeleton(brief, results, false)
	if !strings.Contains(skeleton, "required edit path: b.go") {
		t.Fatalf("correction skeleton should include invalid path b.go:\n%s", skeleton)
	}
	if !strings.Contains(skeleton, "required edit path: a.go") {
		t.Fatalf("correction skeleton should preserve all required paths:\n%s", skeleton)
	}
}

func TestPlanExecutePlanningStateDerivesTextualJSONTerminalTransport(t *testing.T) {
	state := DerivePlanExecutePlanningState(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.go"},
	}, []workspace.ToolResult{{Tool: "read_file", Summary: "Read f1 a.go lines 1-10 (total lines: 10)"}}, PlanExecutePlanningTransport{
		TextualToolParser:  true,
		TerminalOnly:       true,
		ResponseFormatJSON: true,
		EffectiveMaxTokens: 4096,
		TimeoutMillis:      300000,
	})

	if state.Mode != PlanExecutePlanningModeSubmitReady {
		t.Fatalf("mode = %q, want submit_ready", state.Mode)
	}
	if state.TransportMode != PlanExecuteTransportModeTextualJSON {
		t.Fatalf("transport mode = %q, want textual_json_terminal", state.TransportMode)
	}
	if !state.RuntimePolicy.ResponseFormatJSON || state.RuntimePolicy.EffectiveMaxTokens != 4096 || state.RuntimePolicy.TimeoutMillis != 300000 {
		t.Fatalf("runtime policy not preserved: %#v", state.RuntimePolicy)
	}
}

func TestPlanExecutePlanningStateKeepsRequiredSourceBeforeHistoryUntilAllRequiredPathsRead(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.go", "b.go"},
		RequiredEvidence:  []string{requiredEvidenceGitHistoryRationale},
		AllowedTools:      []string{"read_file", "code_navigate", "history_range", "history_symbol", "submit_plan"},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 a.go lines 1-10 (total lines: 10)",
		Output:  "1:package a\n",
	}}

	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode != PlanExecutePlanningModeRequiredSource {
		t.Fatalf("mode = %q, want required_source while b.go is unread", state.Mode)
	}
	for _, want := range []string{"read_file", "code_navigate"} {
		if !slices.Contains(state.ParserVisibleTools, want) {
			t.Fatalf("required-source phase missing %s: %#v", want, state.ParserVisibleTools)
		}
	}
	for _, hidden := range []string{"history_range", "history_symbol", "submit_plan"} {
		if slices.Contains(state.ParserVisibleTools, hidden) {
			t.Fatalf("required-source phase should not expose %s before all required paths are read: %#v", hidden, state.ParserVisibleTools)
		}
	}

	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief:            brief,
		Results:          results,
		BaseAllowedTools: brief.AllowedTools,
	})
	if contract.PhaseID != turnPhaseRequiredSource {
		t.Fatalf("phase = %q, want %q", contract.PhaseID, turnPhaseRequiredSource)
	}
	for _, want := range []string{"read_file", "code_navigate"} {
		if !slices.Contains(contract.AllowedTools, want) {
			t.Fatalf("required-source contract missing %s: %#v", want, contract.AllowedTools)
		}
	}
	for _, hidden := range []string{"history_range", "history_symbol", "submit_plan"} {
		if slices.Contains(contract.AllowedTools, hidden) {
			t.Fatalf("required-source contract should not expose %s before all required paths are read: %#v", hidden, contract.AllowedTools)
		}
	}
	if !slices.Contains(contract.ActionPolicy.AllowedActionClasses, actionRead) {
		t.Fatalf("required-source contract should keep read allowed: %#v", contract.ActionPolicy.AllowedActionClasses)
	}
	if slices.Contains(contract.ActionPolicy.AllowedActionClasses, actionSubmitPlan) {
		t.Fatalf("required-source contract should not allow submit_plan action class: %#v", contract.ActionPolicy.AllowedActionClasses)
	}

	withAllSource := append(results, workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read f2 b.go lines 1-10 (total lines: 10)",
		Output:  "1:package b\n",
	})
	state = DerivePlanExecutePlanningState(brief, withAllSource, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode != PlanExecutePlanningModeHistoryPending {
		t.Fatalf("mode after all required source reads = %q, want history_pending", state.Mode)
	}
	for _, want := range []string{"a.go", "b.go"} {
		if !slices.Contains(state.SelectedEditPaths, want) {
			t.Fatalf("history-pending selected paths missing %s: %#v", want, state.SelectedEditPaths)
		}
	}
}

func TestPlanExecutePlanningStateRequiresHistoryAfterSourceEvidence(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.go"},
		RequiredEvidence:  []string{requiredEvidenceGitHistoryRationale},
		AllowedTools:      []string{"read_file", "history_range", "history_symbol", "submit_plan"},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 a.go lines 1-10 (total lines: 10)",
		Output:  "1:package a\n",
	}}

	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode != PlanExecutePlanningModeHistoryPending {
		t.Fatalf("mode = %q, want history_pending", state.Mode)
	}
	if !slices.Contains(state.ParserVisibleTools, "submit_plan") {
		t.Fatalf("history-pending phase should keep submit_plan visible for the runtime history guard: %#v", state.ParserVisibleTools)
	}
	for _, want := range []string{"read_file", "history_range", "history_symbol"} {
		if !slices.Contains(state.ParserVisibleTools, want) {
			t.Fatalf("history-pending phase missing %s: %#v", want, state.ParserVisibleTools)
		}
	}
	if !slices.Contains(state.AllowedActionClasses, actionRead) {
		t.Fatalf("history-pending phase should expose history tools as read actions: %#v", state.AllowedActionClasses)
	}
	if slices.Contains(state.AllowedActionClasses, "history") {
		t.Fatalf("history-pending phase should not advertise non-canonical history action class: %#v", state.AllowedActionClasses)
	}
	if slices.Contains(state.InvalidActionClasses, actionRead) {
		t.Fatalf("history-pending phase should not invalidate read action class for history tools: %#v", state.InvalidActionClasses)
	}

	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief:            brief,
		Results:          results,
		BaseAllowedTools: brief.AllowedTools,
	})
	if contract.PhaseID != turnPhaseHistoryPending {
		t.Fatalf("phase = %q, want %q", contract.PhaseID, turnPhaseHistoryPending)
	}
	if !slices.Contains(contract.AllowedTools, "submit_plan") {
		t.Fatalf("history-pending contract should keep submit_plan visible for the runtime history guard: %#v", contract.AllowedTools)
	}
	for _, want := range []string{"read_file", "history_range", "history_symbol"} {
		if !slices.Contains(contract.AllowedTools, want) {
			t.Fatalf("history-pending contract missing %s: %#v", want, contract.AllowedTools)
		}
	}
	if !slices.Contains(contract.TerminalOutcomes, "submit_plan") {
		t.Fatalf("history-pending contract should list submit_plan as a terminal outcome: %#v", contract.TerminalOutcomes)
	}
	if !slices.Contains(contract.ActionPolicy.AllowedActionClasses, actionRead) {
		t.Fatalf("history-pending contract should expose history tools as read actions: %#v", contract.ActionPolicy.AllowedActionClasses)
	}
	if slices.Contains(contract.ActionPolicy.AllowedActionClasses, "history") {
		t.Fatalf("history-pending contract should not advertise non-canonical history action class: %#v", contract.ActionPolicy.AllowedActionClasses)
	}
	if slices.Contains(contract.RuntimeCall.ExpressibleActionClasses, "history") {
		t.Fatalf("history-pending runtime contract should only use canonical action classes: %#v", contract.RuntimeCall.ExpressibleActionClasses)
	}
	textualContract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief:             brief,
		Results:           results,
		BaseAllowedTools:  brief.AllowedTools,
		TextualToolParser: true,
		ToolTransportMode: "textual",
	})
	for _, want := range []string{"read_file", "history_range", "history_symbol", "submit_plan"} {
		if !slices.Contains(textualContract.AllowedTools, want) {
			t.Fatalf("textual history-pending contract missing %s: %#v", want, textualContract.AllowedTools)
		}
	}

	withHistory := append(results, workspace.ToolResult{
		Tool:    "history_range",
		Summary: "Read git history for a.go lines 1-10",
		Output:  "commit abc123\n\n    explain current behavior",
	})
	state = DerivePlanExecutePlanningState(brief, withHistory, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode != PlanExecutePlanningModeSubmitReady {
		t.Fatalf("mode after history = %q, want submit_ready", state.Mode)
	}
	if !slices.Contains(state.ParserVisibleTools, "submit_plan") {
		t.Fatalf("submit_ready phase should expose submit_plan after history: %#v", state.ParserVisibleTools)
	}
}

func TestPlanExecutePostReadinessAllowsRequiredHistoryRead(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.go"},
		RequiredEvidence:  []string{requiredEvidenceGitHistoryRationale},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 a.go lines 1-10 (total lines: 10)",
		Output:  "1:package a\n",
	}}
	req := workspace.ToolRequest{
		Tool:      "history_range",
		Path:      "a.go",
		StartLine: 1,
		EndLine:   10,
	}

	if summary, errText, reject := planThenExecutePostReadinessDiscoveryRejection(req, brief, results); reject {
		t.Fatalf("history read after source evidence should remain allowed, got %q: %s", summary, errText)
	}
}

func TestPlanExecuteSubmitBeforeHistoryRedirectsToHistoryRange(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.go"},
			RequiredEvidence:  []string{requiredEvidenceGitHistoryRationale},
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 a.go lines 1-10 (total lines: 10)",
		Output:  "1:package a\n",
	}}
	req := workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{{
			Path:    "a.go",
			OldText: "old",
			NewText: "new",
			Purpose: "update",
		}},
	}

	redirected, _, ok := runner.submitPlanBeforeGitHistoryEvidenceRedirect(req, results, []string{"history_range", "history_symbol"})
	if !ok {
		t.Fatal("expected submit_plan-before-history redirect")
	}
	if redirected.Tool != "history_range" || redirected.Path != "a.go" || redirected.StartLine != 1 || redirected.EndLine != 10 {
		t.Fatalf("unexpected redirect: %#v", redirected)
	}
}

func TestPlanExecuteSubmitBeforeHistoryDoesNotBypassUnreadRequiredSource(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"a.go", "b.go"},
			RequiredEvidence:  []string{requiredEvidenceGitHistoryRationale},
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 a.go lines 1-10 (total lines: 10)",
		Output:  "1:package a\n",
	}}
	req := workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{Path: "a.go", OldText: "old a", NewText: "new a", Purpose: "update a"},
			{Path: "b.go", OldText: "old b", NewText: "new b", Purpose: "update b"},
		},
	}

	if redirected, _, ok := runner.submitPlanBeforeGitHistoryEvidenceRedirect(req, results, []string{"history_range", "history_symbol"}); ok {
		t.Fatalf("submit_plan must not redirect to history while a required path is unread: %#v", redirected)
	}
}

func TestImplementationWriteBeforeHistoryRedirectsToHistoryRange(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:       "implementation",
			RequiredEvidence: []string{requiredEvidenceGitHistoryRationale},
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 a.go lines 1-10 (total lines: 10)",
		Output:  "1:package a\n",
	}}
	req := workspace.ToolRequest{
		Tool:        "edit_file",
		Path:        "a.go",
		OldText:     "old",
		NewText:     "new",
		Description: "update value after checking history",
	}

	redirected, _, ok := runner.writeBeforeGitHistoryEvidenceRedirect(req, results, []string{"history_range", "history_symbol"})
	if !ok {
		t.Fatal("expected write-before-history redirect")
	}
	if redirected.Tool != "history_range" || redirected.Path != "a.go" || redirected.StartLine != 1 || redirected.EndLine != 10 {
		t.Fatalf("unexpected redirect: %#v", redirected)
	}
}

func TestPlanExecuteSubmitPlanExactAnchorGroundsUnscopedPath(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n\nfunc Value() int { return 1 }\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
		},
	}
	req := workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{{
			Path:    "a.go",
			OldText: "return 1",
			NewText: "return 2",
			Purpose: "change value",
		}},
		Rationale: "Change value.",
	}
	guard := runner.preflightPlanThenExecuteSubmitPlanGroundingGuard(req, nil)
	if guard.Intercepted {
		t.Fatalf("exact current-source anchor should ground unscoped submit_plan edit, got %#v", guard)
	}
}

func TestPlanThenExecuteRepeatedSymbolReadRemainsModelOwnedBeforeBudget(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/productworkflow/types.go lines 1-120 (total lines: 200)",
			Output:  "1:package productworkflow\n",
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read CoverageGateResult in internal/productworkflow/types.go lines 20-40\n20:type CoverageGateResult struct {}\n",
		},
	}

	req := workspace.ToolRequest{
		Tool:       "code_navigate",
		Capability: "read_symbol",
		Path:       "internal/productworkflow/types.go",
		Pattern:    "CoverageGateFinding",
		NamedGap:   "need more context",
	}
	state := DerivePlanExecutePlanningActionState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true}, req)
	if len(state.LowValueActions) != 0 {
		t.Fatalf("repeated symbol read should remain model-owned before the candidate-read budget is exhausted, got %#v", state.LowValueActions)
	}
}

func TestPlanThenExecuteManyCandidateReadsRemainModelOwned(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote .idiom/comprehension/views/test.json with 2 candidate file(s)",
			Output:  `{"candidate_files":[{"path":"internal/productworkflow/coverage_gate.go"},{"path":"internal/productworkflow/coverage_gate_test.go"}]}`,
		},
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/productworkflow/coverage_gate.go lines 1-120 (total lines: 391)",
			Output:  "1:package productworkflow\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read f2 internal/productworkflow/coverage_gate_test.go lines 1-80 (total lines: 80)",
			Output:  "1:package productworkflow\n",
		},
	}
	for i := 0; i < 11; i++ {
		results = append(results, workspace.ToolResult{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read Symbol in internal/productworkflow/coverage_gate.go lines 20-40\n20:func Symbol() {}\n",
		})
	}
	if !planThenExecuteTerminalSubmitPlanAllowed(brief, results) {
		t.Fatal("unscoped candidate source evidence should permit submit_plan once all surfaced candidates are read")
	}
	req := workspace.ToolRequest{
		Tool:       "code_navigate",
		Capability: "read_symbol",
		Path:       "internal/productworkflow/coverage_gate.go",
		Pattern:    "AnotherSymbol",
		NamedGap:   "inspect another helper",
	}
	state := DerivePlanExecutePlanningActionState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true}, req)
	if len(state.LowValueActions) != 0 {
		t.Fatalf("many successful source reads must not create a fixed-budget low-value action, got %#v", state.LowValueActions)
	}
}

func TestPlanExecuteManyCandidateReadsDoNotBypassUnreadCandidateSurface(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote .idiom/comprehension/views/test.json with 2 candidate file(s)",
			Output:  `{"candidate_files":[{"path":"pkg/feature.go"},{"path":"pkg/feature_test.go"}]}`,
		},
		{
			Tool:    "read_file",
			Summary: "Read f1 pkg/feature.go lines 1-80 (total lines: 80)",
			Output:  "1:package pkg\n",
		},
	}
	for i := 0; i < 11; i++ {
		results = append(results, workspace.ToolResult{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read Helper in pkg/feature.go lines 20-40\n20:func Helper() {}\n",
		})
	}

	if planThenExecuteTerminalSubmitPlanAllowed(brief, results) {
		t.Fatal("many source reads must not collapse unscoped candidate scope to submit_plan-only while a surfaced candidate remains unread")
	}
	if got := planThenExecuteReadyToSubmitNudge(brief, results); got == "" {
		t.Fatal("many source reads should still emit submit-ready guidance without requiring every unscoped candidate")
	} else if !strings.Contains(got, "Allowed/candidate paths are not coverage obligations") {
		t.Fatalf("readiness nudge should preserve open-candidate semantics:\n%s", got)
	}
}

func TestPlanExecuteRunnerRepeatedCandidateReadDoesNotRecordRunnerRejection(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n\nfunc Value() int { return 1 }\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	replies := []string{
		`{"tool":"read_file","path":"a.go","start_line":1,"end_line":4}`,
		`{"tool":"read_file","path":"a.go","start_line":1,"end_line":4,"named_gap":"need more context"}`,
		"<<QUESTION>>: no concrete selected edit path is required for this low-value-read regression fixture.",
	}
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := replies[len(replies)-1]
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

	var sawLowValue bool
	var eventRoles []string
	var modelCallEvents []map[string]any
	var turnSummary struct {
		RejectedActions          int `json:"rejected_actions"`
		CompletionGateRejections int `json:"completion_gate_rejections"`
	}
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 6},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
		OnEvent: func(event RunEvent) {
			eventRoles = append(eventRoles, event.Role)
			switch event.Role {
			case "plan_then_execute_low_value_action":
				sawLowValue = true
			case "model_call":
				var payload map[string]any
				if err := json.Unmarshal([]byte(event.Content), &payload); err != nil {
					t.Fatalf("decode model_call: %v", err)
				}
				modelCallEvents = append(modelCallEvents, payload)
			case "turn_summary":
				if err := json.Unmarshal([]byte(event.Content), &turnSummary); err != nil {
					t.Fatalf("decode turn_summary: %v", err)
				}
			}
		},
	}
	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan a no-op"}}, llama.ChatOptions{MaxTokens: 2048}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(answer, "<<QUESTION>>") {
		t.Fatalf("expected typed blocker answer, got %q", answer)
	}
	if sawLowValue {
		t.Fatalf("repeated candidate read should remain model-owned before budget exhaustion; saw events %#v", eventRoles)
	}
	if len(modelCallEvents) == 0 {
		t.Fatal("expected model_call telemetry")
	}
	for _, key := range []string{"backend_kind", "transport_mode", "parser_contract", "response_format", "max_tokens", "thinking_policy", "supports_native_tools"} {
		if _, ok := modelCallEvents[0][key]; !ok {
			t.Fatalf("model_call telemetry missing %q: %#v", key, modelCallEvents[0])
		}
	}
	if turnSummary.RejectedActions != 0 || turnSummary.CompletionGateRejections != 0 {
		t.Fatalf("low-value read must not count as runner rejection, got %#v", turnSummary)
	}
}

func TestPlanExecuteRunnerPostGateMissingReadTargetUsesRecovery(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n\nfunc Value() int { return 1 }\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.go"), []byte("package a\n\nfunc Other() int { return 2 }\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	replies := []string{
		`{"tool":"read_file","path":"a.go","start_line":1,"end_line":4}`,
		"Done.",
		`{"tool":"read_file"}`,
		"<<QUESTION>>: Cannot draft a safe plan because the exact missing edit-safety fact was not specified for an unread path.",
	}
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := replies[len(replies)-1]
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

	var sawLowValue bool
	var sawReadTargetFailure bool
	var sawDoomLoop bool
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedEditPaths:  []string{"a.go", "b.go"},
		},
		OnEvent: func(event RunEvent) {
			switch event.Role {
			case "plan_then_execute_low_value_action":
				sawLowValue = true
				if !strings.Contains(event.Content, "candidate_read_missing_target") {
					t.Fatalf("low-value event should name missing target reason: %s", event.Content)
				}
			case "tool_result":
				if strings.Contains(event.Content, "read_file requires id or path") {
					sawReadTargetFailure = true
				}
			case "doom_loop_guard":
				sawDoomLoop = true
			}
		},
	}
	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan a safe change"}}, llama.ChatOptions{MaxTokens: 2048}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(answer, "<<QUESTION>>") {
		t.Fatalf("expected typed blocker answer, got %q", answer)
	}
	if !sawLowValue {
		t.Fatal("expected missing-target read after completion gate to use low-value recovery")
	}
	if sawReadTargetFailure {
		t.Fatal("missing-target read should be recovered before workspace manager execution")
	}
	if sawDoomLoop {
		t.Fatal("missing-target recovery should not reach doom-loop guard")
	}
}

func TestPlanExecuteRunnerPreSourceMissingCodeNavigateTargetStaysModelOwned(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n\nfunc Value() int { return 1 }\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	replies := []string{
		`{"tool":"code_navigate"}`,
		"<<QUESTION>>: Cannot choose a safe plan because the requested navigation omitted a concrete path, capability, or symbol target.",
	}
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := replies[len(replies)-1]
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

	var sawCandidateRead bool
	var sawWorkspaceCodeNavigateFailure bool
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedEditPaths:  []string{"a.go"},
		},
		OnEvent: func(event RunEvent) {
			switch event.Role {
			case "tool_result":
				if strings.Contains(event.Content, "capability is required") || strings.Contains(event.Content, "path is required") {
					sawWorkspaceCodeNavigateFailure = true
				}
			}
		},
	}
	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan a safe change"}}, llama.ChatOptions{MaxTokens: 2048}, nil, nil, func(event ToolResultEvent) {
		if event.Request.Tool == "read_file" && event.Request.Path == "a.go" && event.Result.Error == "" {
			sawCandidateRead = true
		}
		if event.Request.Tool == "code_navigate" && (strings.Contains(event.Result.Error, "capability is required") || strings.Contains(event.Result.Error, "path is required")) {
			sawWorkspaceCodeNavigateFailure = true
		}
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(answer, "<<QUESTION>>") {
		t.Fatalf("expected typed blocker answer, got %q", answer)
	}
	if sawCandidateRead {
		t.Fatal("targetless code_navigate must not be rewritten into a host-selected candidate read")
	}
	if sawWorkspaceCodeNavigateFailure {
		t.Fatal("targetless code_navigate should be recovered before workspace manager execution")
	}
}

func TestPlanThenExecuteCandidateReadFileIDIsConcreteTarget(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:       PlanThenExecutePlannerTaskFamily,
		AllowedEditPaths: []string{"a.go", "b.go"},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 a.go lines 1-4 (total lines: 4)",
		Output:  "1:package a\n",
	}}

	req := workspace.ToolRequest{
		Tool:      "read_file",
		ID:        "f3",
		StartLine: 1,
		EndLine:   80,
		NamedGap:  "inspect discovered file handle before selecting the plan edit",
	}
	reason, prompt, lowValue := planThenExecuteCandidateLowValueAction(req, brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if lowValue && reason == "candidate_read_missing_target" {
		t.Fatalf("read_file id target must not be classified as missing target:\n%s", prompt)
	}
}

func TestPlanThenExecuteCandidateCodeNavigateWorkspaceSymbolTargetIsConcrete(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:       PlanThenExecutePlannerTaskFamily,
		AllowedEditPaths: []string{"a.go", "b.go"},
	}
	req := workspace.ToolRequest{
		Tool:       "code_navigate",
		Capability: "workspace_symbol",
		Pattern:    "Value",
		NamedGap:   "locate candidate symbol before selecting edit path",
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 a.go lines 1-4 (total lines: 4)",
		Output:  "1:package a\n",
	}}
	if planThenExecuteReadRequestMissingConcreteTarget(req) {
		t.Fatal("workspace_symbol code_navigate with pattern should be treated as a concrete symbol target")
	}
	reason, prompt, lowValue := planThenExecuteCandidateLowValueAction(req, brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if lowValue && reason == "candidate_read_missing_target" {
		t.Fatalf("workspace_symbol code_navigate with pattern must not be classified as missing target:\n%s", prompt)
	}
}

func TestPlanExecuteMissingTargetReadRedirectDoesNotSelectCandidate(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "context_pack",
		Summary: "Context pack wrote .idiom/comprehension/views/test.json with 2 candidate file(s)",
		Output:  `{"candidate_files":[{"path":"internal/productworkflow/coverage_gate.go"},{"path":"cmd/idiom/audit.go"}]}`,
	}}

	if req, raw, ok := runner.planThenExecuteMissingTargetReadRedirect(workspace.ToolRequest{Tool: "read_file"}, results); ok {
		t.Fatalf("targetless read_file must stay model-owned instead of redirecting to packet candidate, req=%#v raw=%s", req, raw)
	}
}

func TestPlanExecuteMissingTargetReadRedirectSkipsRequiredSourceScope(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"required.go"},
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "context_pack",
		Summary: "Context pack wrote .idiom/comprehension/views/test.json with 1 candidate file(s)",
		Output:  `{"candidate_files":[{"path":"other.go"}]}`,
	}}

	if req, _, ok := runner.planThenExecuteMissingTargetReadRedirect(workspace.ToolRequest{Tool: "read_file"}, results); ok {
		t.Fatalf("required source scope must not redirect targetless read_file to packet candidate: %#v", req)
	}
}

func TestPlanExecuteNonToolCandidateRecoveryDoesNotReadContextPackCandidate(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "context_pack",
		Summary: "Context pack wrote .idiom/comprehension/views/test.json with 2 candidate file(s)",
		Output:  `{"candidate_files":[{"path":"internal/productworkflow/coverage_gate.go"},{"path":"cmd/idiom/audit.go"}]}`,
	}}

	if req, raw, ok := runner.deterministicPlanExecuteNonToolCandidateRecovery("I need to inspect the relevant source before planning.", results); ok {
		t.Fatalf("non-terminal planner prose must not trigger a host-selected candidate read, req=%#v raw=%s", req, raw)
	}
}

func TestPlanExecuteNonToolCandidateRecoveryLeavesNextCandidateModelOwned(t *testing.T) {
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
			Summary: "Context pack wrote .idiom/comprehension/views/test.json with 2 candidate file(s)",
			Output:  `{"candidate_files":[{"path":"cmd/idiom/product_workflow.go"},{"path":"internal/productworkflow/coverage_gate.go"}]}`,
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n1515:func runProductTDD() {}\n",
		},
		{
			Tool:    "completion_gate",
			Summary: "Plan-execute completion gate rejected final answer",
			Error:   "plan-execute planner phase requires a successful submit_plan tool result before final answer",
		},
	}
	if read := readPathsFromResults(results); len(read) == 0 {
		t.Fatalf("fixture should expose code_navigate source-body evidence, got %#v", read)
	}
	if unread := unreadPlanThenExecuteCandidateScopePathsForRecovery(runner.RuntimeBrief, results, 4); len(unread) == 0 {
		t.Fatalf("fixture should expose an unread context-pack candidate, got none")
	}
	if req, raw, ok := runner.deterministicNoWriteRecoveryUnreadCandidateRead(results); ok {
		t.Fatalf("plan-execute unread candidate recovery must not host-select the next path, req=%#v raw=%s", req, raw)
	}

	if req, raw, ok := runner.deterministicPlanExecuteNonToolCandidateRecovery("I need to inspect one more source surface before planning.", results); ok {
		t.Fatalf("non-terminal planner prose must not trigger the next host-selected candidate read, req=%#v raw=%s", req, raw)
	}
}

func TestPlanExecuteNonToolCandidateRecoverySkipsTerminalReply(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	results := []workspace.ToolResult{{
		Tool:   "context_pack",
		Output: `{"candidate_files":[{"path":"internal/productworkflow/coverage_gate.go"}]}`,
	}}

	if req, raw, ok := runner.deterministicPlanExecuteNonToolCandidateRecovery("<<QUESTION>>: missing exact source evidence", results); ok {
		t.Fatalf("terminal blocker reply must not be converted into a read, req=%#v raw=%s", req, raw)
	}
}

func TestPlanThenExecuteCandidatePostReadinessSamePathReadRemainsModelOwned(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:       PlanThenExecutePlannerTaskFamily,
		AllowedEditPaths: []string{"cmd/idiom/product_workflow_test.go", "internal/productworkflow/coverage_gate.go"},
	}
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output:  "route_summary: Read validTestImplementationLedgerJSONL in cmd/idiom/product_workflow_test.go lines 3636-3678\n\n3636:func validTestImplementationLedgerJSONL() string {}\n",
	}}

	req := workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "cmd/idiom/product_workflow_test.go",
		StartLine: 3636,
		EndLine:   3678,
		NamedGap:  "inspect the helper body again",
	}
	if summary, errorText, reject := planThenExecuteCandidatePostReadinessDiscoveryRejection(req, brief, results); reject {
		t.Fatalf("same candidate path reread should not be a hard rejection, got %q: %s", summary, errorText)
	}
	reason, prompt, lowValue := planThenExecuteCandidateLowValueAction(req, brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if lowValue {
		t.Fatalf("same candidate path reread should remain model-owned before budget exhaustion, reason=%q prompt=\n%s", reason, prompt)
	}
}

func TestPlanThenExecuteCandidatePostReadinessDifferentPathDoesNotRequireNamedGap(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:       PlanThenExecutePlannerTaskFamily,
		AllowedEditPaths: []string{"internal/productworkflow/coverage_gate.go", "cmd/idiom/product_workflow.go"},
	}
	results := []workspace.ToolResult{{
		Tool:    "context_pack",
		Summary: "Context pack wrote .idiom/comprehension/views/test.json with 2 candidate file(s)",
		Output:  `{"candidate_files":[{"path":"internal/productworkflow/coverage_gate.go"},{"path":"cmd/idiom/product_workflow.go"}]}`,
	}, {
		Tool:    "read_file",
		Summary: "Read f1 internal/productworkflow/coverage_gate.go lines 1-125 (total lines: 391)",
		Output:  "1:package productworkflow\n",
	}}

	req := workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "cmd/idiom/product_workflow.go",
		StartLine: 1500,
		EndLine:   1650,
	}
	reason, prompt, lowValue := planThenExecuteCandidateLowValueAction(req, brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if lowValue {
		t.Fatalf("different candidate read should not require a harness-inferred named gap before budget exhaustion, reason=%q prompt=\n%s", reason, prompt)
	}

	req.NamedGap = "exact coverage-gate call site before wiring authority preflight"
	req.WhyNotAnswering = "need the current command path before drafting the edit"
	if _, _, lowValue := planThenExecuteCandidateLowValueAction(req, brief, results, PlanExecutePlanningTransport{TextualToolParser: true}); lowValue {
		t.Fatal("different candidate read with a named edit-safety gap should remain available")
	}
}

func TestPlanThenExecuteCandidatePostReadinessAllowsSamePathContinuationRead(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:       PlanThenExecutePlannerTaskFamily,
		AllowedEditPaths: []string{"internal/productworkflow/coverage_gate.go"},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/productworkflow/coverage_gate.go lines 1-125 (total lines: 391)",
		Output:  "1:package productworkflow\n",
	}}

	_, _, reject := planThenExecuteCandidatePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "internal/productworkflow/coverage_gate.go",
		StartLine: 126,
		EndLine:   250,
	}, brief, results)
	if reject {
		t.Fatal("bounded unread same-path continuation should remain available for larger candidate files")
	}
}

func TestPlanThenExecuteCandidatePostReadinessAllowsSmallFullReadAfterPartialRead(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{TaskFamily: PlanThenExecutePlannerTaskFamily}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/productworkflow/coverage_gate.go lines 1-125 (total lines: 391)",
		Output:  "1:package productworkflow\n",
	}}

	_, _, reject := planThenExecuteCandidatePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool: "read_file",
		Path: "internal/productworkflow/coverage_gate.go",
	}, brief, results)
	if reject {
		t.Fatal("full-file read of a small partially read candidate should be treated as continuation, not duplicate evidence")
	}
}

func TestPlanThenExecuteCandidatePostReadinessAllowsUnboundedSmallPartialSamePathRead(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:       PlanThenExecutePlannerTaskFamily,
		AllowedEditPaths: []string{"internal/productworkflow/validate.go"},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/productworkflow/validate.go lines 300-350 (total lines: 391)",
		Output:  "300:func ValidateProductWorkflow() {}\n",
	}}

	req := workspace.ToolRequest{
		Tool: "read_file",
		Path: "internal/productworkflow/validate.go",
	}
	if summary, errorText, reject := planThenExecuteCandidatePostReadinessDiscoveryRejection(req, brief, results); reject {
		t.Fatalf("unbounded reread of a small partially read candidate should not hard reject, got %q: %s", summary, errorText)
	}
	if _, _, lowValue := planThenExecuteCandidateLowValueAction(req, brief, results, PlanExecutePlanningTransport{TextualToolParser: true}); lowValue {
		t.Fatal("unbounded reread of a partially read candidate should remain available as same-path continuation")
	}
}

func TestPlanThenExecuteCandidatePostReadinessAllowsPriorReadContinuationOutsideCurrentCandidateSet(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{TaskFamily: PlanThenExecutePlannerTaskFamily}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/productworkflow/coverage_gate.go lines 1-125 (total lines: 391)",
			Output:  "1:package productworkflow\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read f2 internal/productworkflow/validate.go lines 1-300 (total lines: 1449)",
			Output:  "1:package productworkflow\n",
		},
	}

	_, _, reject := planThenExecuteCandidatePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "internal/productworkflow/coverage_gate.go",
		StartLine: 125,
		EndLine:   250,
	}, brief, results)
	if reject {
		t.Fatal("bounded continuation of a previously read path should not depend on current candidate ranking")
	}
}

func TestPlanThenExecuteCandidatePostReadinessAllowsSymbolReadOnPreviouslyReadPath(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{TaskFamily: PlanThenExecutePlannerTaskFamily}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/productworkflow/coverage_gate.go lines 1-391 (total lines: 391)",
			Output:  "1:package productworkflow\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read f2 internal/productworkflow/validate.go lines 1-300 (total lines: 1449)",
			Output:  "1:package productworkflow\n",
		},
	}

	_, _, reject := planThenExecuteCandidatePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:       "code_navigate",
		Capability: "read_symbol",
		Path:       "internal/productworkflow/validate.go",
		Pattern:    "ValidateADRCrossLinks",
	}, brief, results)
	if reject {
		t.Fatal("symbol-body read on a previously read candidate path should remain available")
	}
}

func TestPlanThenExecuteCandidatePostReadinessRejectsNonBodyNavigationOnReadyPath(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{TaskFamily: PlanThenExecutePlannerTaskFamily}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/productworkflow/coverage_gate.go lines 1-391 (total lines: 391)",
		Output:  "1:package productworkflow\n",
	}}

	summary, errorText, reject := planThenExecuteCandidatePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:       "code_navigate",
		Capability: "find_symbol",
		Path:       "internal/productworkflow/coverage_gate.go",
		Pattern:    "CoverageGateFinding",
	}, brief, results)
	if !reject {
		t.Fatal("non-body navigation on an already sourced candidate should be rejected after readiness")
	}
	if summary != "Plan-execute candidate source already ready" {
		t.Fatalf("unexpected summary %q", summary)
	}
	for _, want := range []string{"already has source-body evidence", "submit_plan", "bounded source-body continuation", "<<QUESTION>>"} {
		if !strings.Contains(errorText, want) {
			t.Fatalf("rejection missing %q:\n%s", want, errorText)
		}
	}
}

func TestPlanExecuteSubmitOnlySurfaceDoesNotReopenHiddenNavigation(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:   PlanThenExecutePlannerTaskFamily,
		AllowedTools: []string{"code_navigate", "submit_plan"},
	}
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Candidate source already ready",
		Code:    planThenExecuteResultCandidateSourceReady,
		Error:   "terminal planner recovery active",
	}}
	req := workspace.ToolRequest{
		Tool:       "code_navigate",
		Capability: "read_symbol",
		Path:       "internal/productworkflow/coverage_gate.go",
		Pattern:    "CoverageGate",
	}

	allowed := runnerPlanExecuteTextualAllowedToolsForRequest([]string{"submit_plan"}, brief, results, req)
	if len(allowed) != 1 || allowed[0] != "submit_plan" {
		t.Fatalf("submit-only terminal surface reopened hidden navigation: %#v", allowed)
	}
}

func TestPlanThenExecuteCandidatePostReadinessAllowsDifferentNamedCandidateRead(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:       PlanThenExecutePlannerTaskFamily,
		AllowedEditPaths: []string{"cmd/idiom/product_workflow_test.go", "internal/productworkflow/coverage_gate.go"},
	}
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output:  "route_summary: Read validTestImplementationLedgerJSONL in cmd/idiom/product_workflow_test.go lines 3636-3678\n\n3636:func validTestImplementationLedgerJSONL() string {}\n",
	}}

	_, _, reject := planThenExecuteCandidatePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "internal/productworkflow/coverage_gate.go",
		StartLine: 1,
		EndLine:   120,
		NamedGap:  "exact current gate signature before deciding whether this second candidate needs an edit",
	}, brief, results)
	if reject {
		t.Fatal("a different unread allowed candidate with a concrete named gap should remain available")
	}
}

func TestPlanThenExecuteCandidatePostReadinessAllowsDifferentBoundedCandidateRead(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:       PlanThenExecutePlannerTaskFamily,
		AllowedEditPaths: []string{"pkg/feature.go", "pkg/feature_test.go"},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 pkg/feature.go lines 1-4",
		Output:  "1:package pkg\n2:\n3:func Hardened() bool { return true }\n",
	}}

	_, _, reject := planThenExecuteCandidatePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "pkg/feature_test.go",
		StartLine: 1,
		EndLine:   4,
	}, brief, results)
	if reject {
		t.Fatal("a different unread allowed candidate with a bounded range should remain available for source-satisfied evidence")
	}
}

func TestPlanThenExecuteCandidatePostGateRejectsOutOfScopeRead(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:       PlanThenExecutePlannerTaskFamily,
		AllowedEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/productworkflow/coverage_gate.go"},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote view",
			Output:  `{"candidate_files":[{"path":"cmd/idiom/product_workflow.go"},{"path":"internal/productworkflow/coverage_gate.go"}]}`,
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 900-1080\n900:func runProductTDD() {}\n",
		},
		{
			Tool:    "completion_gate",
			Summary: "Plan-execute completion gate rejected final answer",
			Error:   "plan-execute planner phase requires a successful submit_plan tool result before final answer",
		},
	}
	summary, errorText, reject := planThenExecuteCandidatePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "cmd/idiom/rendering.go",
		StartLine: 1,
		EndLine:   180,
	}, brief, results)
	if !reject {
		t.Fatal("post-gate hallucinated read outside explicit allowed candidates should be rejected")
	}
	if summary != "Plan-execute post-gate read outside candidate scope" {
		t.Fatalf("unexpected summary %q", summary)
	}
	for _, want := range []string{"completion gate", "not an explicit allowed candidate path", "cmd/idiom/product_workflow.go", "internal/productworkflow/coverage_gate.go", "submit_plan"} {
		if !strings.Contains(errorText, want) {
			t.Fatalf("rejection missing %q:\n%s", want, errorText)
		}
	}
}

func TestPlanThenExecuteCandidatePostGateDoesNotTreatContextPackAsScopeBoundary(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{TaskFamily: PlanThenExecutePlannerTaskFamily}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote view",
			Output:  `{"candidate_files":[{"path":"cmd/idiom/product_evidence.go"},{"path":"internal/productworkflow/coverage_gate.go"}]}`,
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read p912EvaluatePriorGateEvidence in cmd/idiom/product_evidence.go lines 1-120\n1:func p912EvaluatePriorGateEvidence() {}\n",
		},
		{
			Tool:    "completion_gate",
			Summary: "Plan-execute completion gate rejected final answer",
			Error:   "plan-execute planner phase requires a successful submit_plan tool result before final answer",
		},
	}

	_, _, reject := planThenExecuteCandidatePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:       "code_navigate",
		Capability: "file_outline",
		Path:       "cmd/idiom/product_workflow.go",
	}, brief, results)
	if reject {
		t.Fatal("context-pack candidates are evidence hints, not an open-candidate scope boundary")
	}
}

func TestPlanThenExecuteCandidatePostGateAllowsTypedCandidateRead(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{TaskFamily: PlanThenExecutePlannerTaskFamily}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote view",
			Output:  `{"candidate_files":[{"path":"cmd/idiom/product_workflow.go"},{"path":"internal/productworkflow/coverage_gate.go"}]}`,
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 900-1080\n900:func runProductTDD() {}\n",
		},
		{
			Tool:    "completion_gate",
			Summary: "Plan-execute completion gate rejected final answer",
			Error:   "plan-execute planner phase requires a successful submit_plan tool result before final answer",
		},
	}
	_, _, reject := planThenExecuteCandidatePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "internal/productworkflow/coverage_gate.go",
		StartLine: 1,
		EndLine:   180,
	}, brief, results)
	if reject {
		t.Fatal("post-gate bounded read of a typed candidate should remain available")
	}
}

func TestPlanThenExecuteCandidatePostReadinessAllowsRejectedAnchorRefresh(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:       PlanThenExecutePlannerTaskFamily,
		AllowedEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/productworkflow/coverage_gate.go"},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/productworkflow/coverage_gate.go lines 1-360 (total lines: 360)",
			Output:  "1:package productworkflow\n",
		},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "invalid edit anchor(s): `internal/productworkflow/coverage_gate.go: old_text not found in current source`.",
		},
	}

	_, _, reject := planThenExecuteCandidatePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:       "code_navigate",
		Path:       "internal/productworkflow/coverage_gate.go",
		Capability: "read_symbol",
		Pattern:    "EvaluateCoverageGate",
	}, brief, results)
	if reject {
		t.Fatal("candidate-only invalid-anchor rejection should allow one bounded source refresh before another submit_plan")
	}
}

func TestPlanThenExecuteCandidatePostReadinessNamesMissingAnchorCapability(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:       PlanThenExecutePlannerTaskFamily,
		AllowedEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/productworkflow/coverage_gate.go"},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/productworkflow/coverage_gate.go lines 1-360 (total lines: 360)",
			Output:  "1:package productworkflow\n",
		},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "invalid edit anchor(s): `cmd/idiom/product_workflow.go: old_text not found in current source`.",
		},
	}

	summary, errorText, reject := planThenExecuteCandidatePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:    "code_navigate",
		Path:    "cmd/idiom/product_workflow.go",
		Pattern: "runProductTDD",
	}, brief, results)
	if !reject {
		t.Fatal("code_navigate anchor refresh without capability should be rejected with a targeted correction")
	}
	if summary != "Anchor refresh capability required" {
		t.Fatalf("unexpected summary %q", summary)
	}
	for _, want := range []string{"source-body capability", "read_symbol", "concrete symbol/pattern", "invalid edit anchor"} {
		if !strings.Contains(errorText, want) {
			t.Fatalf("targeted rejection missing %q:\n%s", want, errorText)
		}
	}
	if strings.Contains(errorText, `{"tool":"code_navigate"`) {
		t.Fatalf("targeted rejection must not include copyable JSON request:\n%s", errorText)
	}
	if strings.Contains(errorText, "Allowed paths are not required coverage") {
		t.Fatalf("missing capability should not use the generic candidate-ready rejection:\n%s", errorText)
	}
}

func TestPlanThenExecuteCandidatePostReadinessDoesNotPromoteAllowedPathsToSubmitCoverage(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:       PlanThenExecutePlannerTaskFamily,
			AllowedEditPaths: []string{"a.txt", "b.txt"},
		},
	}
	got := runner.preflightPlanThenExecuteSubmitPlanCoverageGuard(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{{
			Path:            "a.txt",
			InsertAfterLine: 1,
			Content:         "added\n",
			Purpose:         "add needed behavior",
		}},
	})
	if got.Intercepted {
		t.Fatalf("allowed candidate scope must constrain paths without requiring coverage for every allowed path: %#v", got)
	}
}

func TestPlanThenExecuteReadyToSubmitNudgeStandsDownAfterSubmit(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.txt"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.txt lines 1-10"},
		{Tool: "submit_plan", Summary: "Plan submitted with 1 edit(s); the executor phase will dispatch them."},
	}
	if got := planThenExecuteReadyToSubmitNudge(brief, results); got != "" {
		t.Fatalf("nudge must stand down once submit_plan succeeded; got %q", got)
	}
}

func TestPlanThenExecuteReadyToSubmitNudgeIgnoresOtherLanes(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		RequiredEditPaths: []string{"a.txt"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.txt lines 1-10"},
	}
	if got := planThenExecuteReadyToSubmitNudge(brief, results); got != "" {
		t.Fatalf("nudge must only fire under plan_then_execute_planner; got %q", got)
	}
}

func TestPlanThenExecuteReadyToSubmitNudgeRecognizesLanguageSymbolReads(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"native/src/foo.cpp"},
	}
	results := []workspace.ToolResult{
		// cpp_read_symbol summary shape: "Read <symbol> in <path> lines A-B"
		{Tool: "cpp_read_symbol", Summary: "Read MyClass::method in native/src/foo.cpp lines 10-30"},
	}
	if got := planThenExecuteReadyToSubmitNudge(brief, results); got == "" {
		t.Fatalf("nudge should recognize language-symbol read summaries as covering the path")
	}
}

func TestCanonicalMutationBlockerFinalAnswerExemptsPlanExecute(t *testing.T) {
	// Decorator-side exemption: for plan-execute planner phase, the
	// model's clean "plan submitted" answer must not be rewritten into
	// a "required edit paths remain unchanged" blocker just because the
	// planner phase has no changed_paths. The executor phase (separate
	// process) produces the file changes.
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.txt", "b.txt"},
	}
	results := []workspace.ToolResult{
		{Tool: "submit_plan", Summary: "Plan submitted with 2 edit(s); the executor phase will dispatch them."},
	}
	got, decorated := canonicalMutationBlockerFinalAnswer(brief, results, "Plan submitted with one edit.")
	if decorated {
		t.Fatalf("decorator must NOT synthesize a blocker for plan-execute with a successful submit_plan; got %q", got)
	}
}

func TestCanonicalMutationBlockerFinalAnswerStillFiresForRegularImplementation(t *testing.T) {
	// Sanity check: the plan-execute exemption must not weaken the
	// decorator for normal implementation lanes.
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.txt"},
	}
	got, decorated := canonicalMutationBlockerFinalAnswer(brief, nil, "Done.")
	if !decorated {
		t.Fatalf("regular implementation lane with no edits should be decorated to a blocker; got %q", got)
	}
	if !strings.Contains(got, "Required edit path") {
		t.Fatalf("blocker should reference required edit paths; got %q", got)
	}
}

func TestPlanThenExecuteShortCircuitAnswerFiresAfterSuccessfulSubmit(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.txt", "b.txt"},
	}
	req := workspace.ToolRequest{Tool: "submit_plan"}
	result := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "Plan submitted with 2 edit(s); the executor phase will dispatch them.",
		Output:  "plan_path: /tmp/plans/x.plan.json\nedits: 2\nschema_version: " + workspace.PlanArtifactSchemaVersion,
	}
	answer, ok := planThenExecuteShortCircuitAnswer(brief, req, result)
	if !ok {
		t.Fatalf("short-circuit must fire after successful submit_plan; got !ok")
	}
	if !strings.Contains(answer, "Plan submitted with 2 edit(s)") {
		t.Fatalf("synthetic answer must include the result summary; got %q", answer)
	}
	if !strings.Contains(answer, "executor phase") {
		t.Fatalf("synthetic answer must reference the executor phase; got %q", answer)
	}
}

func TestPlanThenExecuteShortCircuitSkipsErrors(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}
	req := workspace.ToolRequest{Tool: "submit_plan"}
	failure := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected",
		Error:   "submit_plan requires at least one edit in the edits array",
	}
	if _, ok := planThenExecuteShortCircuitAnswer(brief, req, failure); ok {
		t.Fatalf("short-circuit must NOT fire on a failed submit_plan — let the runner recover normally")
	}
}

func TestPlanThenExecuteNoopSubmitPlanDoesNotSynthesizeSourceSatisfiedAnswer(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		PermissionProfile: "guarded_workspace_write",
		Objective:         "reject temp symlinks and add regression tests proving no partial mutation",
	}
	req := workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{Path: "internal/productworkflow/migration_apply.go", OldText: "same", NewText: "same", Purpose: "source"},
			{Path: "internal/productworkflow/migration_apply_test.go", OldText: "same", NewText: "same", Purpose: "test"},
		},
	}
	result := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected",
		Error:   "edits[0]: old_text and new_text are identical; edits[1]: old_text and new_text are identical; submit real edits, or answer `Decision: source-satisfied`",
	}

	if answer, ok := planThenExecuteNoopSubmitPlanSourceSatisfiedAnswer(brief, req, result, nil); ok {
		t.Fatalf("rejected no-op submit_plan must remain an invalid attempt, got synthesized answer:\n%s", answer)
	}
}

func TestPlanThenExecuteEmptySubmitPlanSourceSatisfiedSynthesizesAnswerWithRequiredEvidence(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"internal/writerroute/decision_test.go"},
	}
	req := workspace.ToolRequest{
		Tool:      "submit_plan",
		Rationale: "The required test file already contains the complete, correct test implementations, satisfying the source-satisfied condition with per-path evidence.",
		Edits:     []workspace.PlanEdit{},
	}
	result := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected: required edit path coverage mismatch",
		Error:   "submit_plan must include at least one edit entry for every required_edit_paths coverage entry; missing required edit path(s): `internal/writerroute/decision_test.go`.",
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/writerroute/decision_test.go lines 1-80 (total lines: 80)",
			Output:  "package writerroute\n\nfunc TestWriterRouteDecision_NormalizationAndStableID(t *testing.T) {}\n",
		},
	}

	if answer, ok := planThenExecuteEmptySubmitPlanSourceSatisfiedAnswer(brief, req, result, results); ok {
		t.Fatalf("empty submit_plan must remain invalid even with source-satisfied rationale:\n%s", answer)
	}
}

func TestPlanThenExecuteEmptySubmitPlanSourceSatisfiedAcceptsExistingFileRationale(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"internal/writerroute/decision_test.go"},
	}
	req := workspace.ToolRequest{
		Tool:      "submit_plan",
		Rationale: "The existing decision_test.go already implements the required normalization, EventFields, and JSON mapping tests matching the spec, so the file satisfies the objective.",
		Edits:     []workspace.PlanEdit{},
	}
	result := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected: required edit path coverage mismatch",
		Error:   "submit_plan must include at least one edit entry for every required_edit_paths coverage entry; missing required edit path(s): `internal/writerroute/decision_test.go`.",
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f2 internal/writerroute/decision_test.go lines 1-200 (total lines: 200)",
			Output:  "package writerroute\n\nfunc TestWriterRouteDecision_NormalizationAndStableID(t *testing.T) {}\n",
		},
	}

	if answer, ok := planThenExecuteEmptySubmitPlanSourceSatisfiedAnswer(brief, req, result, results); ok {
		t.Fatalf("existing-file rationale inside empty submit_plan must remain invalid:\n%s", answer)
	}
}

func TestPlanExecuteRunnerShortCircuitsEmptySubmitPlanSourceSatisfied(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}
	replies := []string{
		`{"tool":"read_file","path":"a.txt","start_line":1,"end_line":1}`,
		`{"tool":"submit_plan","rationale":"The current source already contains the requested final state and is source-satisfied; no changes are needed.","edits":[]}`,
		`{"tool":"submit_plan","rationale":"Corrected plan after invalid empty submit_plan.","edits":[{"path":"a.txt","old_text":"before\n","new_text":"after\n","purpose":"replace fixture text"}]}`,
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

	var sawShortCircuit bool
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 6},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "plan_then_execute_source_satisfied_short_circuit" {
				sawShortCircuit = true
			}
		},
	}
	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan edits"}}, llama.ChatOptions{
		MaxTokens: 2048,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "auto",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if requests != len(replies) {
		t.Fatalf("runner should require an explicit corrected terminal action after empty submit_plan, got %d request(s)", requests)
	}
	if sawShortCircuit {
		t.Fatal("empty submit_plan must not short-circuit as source-satisfied")
	}
	for _, want := range []string{"Plan submitted"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("answer missing %q:\n%s", want, answer)
		}
	}
}

func TestPlanThenExecuteEmptySubmitPlanSourceSatisfiedRequiresAnchorReadyEvidence(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"internal/writerroute/decision_test.go"},
	}
	req := workspace.ToolRequest{
		Tool:      "submit_plan",
		Rationale: "The current source is source-satisfied and no changes are needed.",
	}
	result := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected: required edit path coverage mismatch",
		Error:   "submit_plan must include at least one edit entry for every required_edit_paths coverage entry; missing required edit path(s): `internal/writerroute/decision_test.go`.",
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/writerroute/decision_test.go lines 1-20 (total lines: 120)",
			Output:  "package writerroute\n",
		},
	}

	if answer, ok := planThenExecuteEmptySubmitPlanSourceSatisfiedAnswer(brief, req, result, results); ok {
		t.Fatalf("body-window evidence must not synthesize source-satisfied closeout:\n%s", answer)
	}
}

func TestPlanThenExecuteEmptySubmitPlanSourceSatisfiedSkipsRequiredSnippetBriefs(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"internal/writerroute/decision_test.go"},
		RequiredEditSnippets: []workerruntime.RequiredEditSnippet{
			{Path: "internal/writerroute/decision_test.go", Content: "func TestWriterRouteDecision_NormalizationAndStableID(t *testing.T) {}"},
		},
	}
	req := workspace.ToolRequest{
		Tool:      "submit_plan",
		Rationale: "The required test file already contains the complete, correct test implementations, satisfying the source-satisfied condition with per-path evidence.",
	}
	result := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected: required edit path coverage mismatch",
		Error:   "submit_plan must include at least one edit entry for every required_edit_paths coverage entry; missing required edit path(s): `internal/writerroute/decision_test.go`.",
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/writerroute/decision_test.go lines 1-80 (total lines: 80)",
			Output:  "package writerroute\n\nfunc TestDifferentBehavior(t *testing.T) {}\n",
		},
	}

	if answer, ok := planThenExecuteEmptySubmitPlanSourceSatisfiedAnswer(brief, req, result, results); ok {
		t.Fatalf("empty submit_plan repair must not bypass required_edit_snippets:\n%s", answer)
	}
}

func TestPlanThenExecuteEmptySubmitPlanWithoutSourceSatisfiedIntentStaysRejected(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.txt"},
	}
	req := workspace.ToolRequest{
		Tool:      "submit_plan",
		Rationale: "I could not decide the edit shape.",
	}
	result := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected: required edit path coverage mismatch",
		Error:   "submit_plan must include at least one edit entry for every required_edit_paths coverage entry; missing required edit path(s): `a.txt`.",
	}
	results := []workspace.ToolResult{{Tool: "read_file", Summary: "Read f1 a.txt lines 1-1 (total lines: 1)", Output: "before\n"}}

	if answer, ok := planThenExecuteEmptySubmitPlanSourceSatisfiedAnswer(brief, req, result, results); ok {
		t.Fatalf("empty submit_plan without source-satisfied intent must not synthesize closeout:\n%s", answer)
	}
}

func TestPlanThenExecuteEmptySubmitPlanNegatedSourceSatisfiedRationaleStaysRejected(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.txt"},
	}
	result := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected: required edit path coverage mismatch",
		Error:   "submit_plan must include at least one edit entry for every required_edit_paths coverage entry; missing required edit path(s): `a.txt`.",
	}
	results := []workspace.ToolResult{{Tool: "read_file", Summary: "Read f1 a.txt lines 1-1 (total lines: 1)", Output: "before\n"}}

	for _, rationale := range []string{
		"The current source is not source-satisfied because it does not contain the requested final state.",
		"The current source is not yet source-satisfied; it still needs edits to reach the requested final state.",
		"The current source is not yet source-satisfied; no changes were chosen.",
		"The required path source-satisfied check requires edits before the final state is complete.",
	} {
		req := workspace.ToolRequest{
			Tool:      "submit_plan",
			Rationale: rationale,
		}
		if answer, ok := planThenExecuteEmptySubmitPlanSourceSatisfiedAnswer(brief, req, result, results); ok {
			t.Fatalf("negated source-satisfied rationale must not synthesize closeout for %q:\n%s", rationale, answer)
		}
	}
}

func TestPlanThenExecuteNoopSubmitPlanDoesNotSourceSatisfyUnvalidatedRequiredPaths(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
		Objective:  "fix product coverage gate regressions and tests",
		RequiredEditPaths: []string{
			"cmd/idiom/product_workflow.go",
			"cmd/idiom/product_workflow_test.go",
			"internal/productworkflow/coverage_gate.go",
			"internal/productworkflow/coverage_gate_test.go",
		},
	}
	req := workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{Path: "cmd/idiom/product_workflow.go", OldText: "before", NewText: "after", Purpose: "source"},
			{Path: "internal/productworkflow/coverage_gate.go", OldText: "same", NewText: "same", Purpose: "source"},
			{Path: "cmd/idiom/product_workflow_test.go", OldText: "before", NewText: "after", Purpose: "test"},
			{Path: "internal/productworkflow/coverage_gate_test.go", OldText: "before", NewText: "after", Purpose: "test"},
		},
	}
	result := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected",
		Error:   "edits[1]: edit would not change internal/productworkflow/coverage_gate.go; submit real edits, or answer `Decision: source-satisfied`",
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f3 internal/productworkflow/coverage_gate.go lines 1-337"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read runProduct in cmd/idiom/product_workflow.go lines 1-80"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read TestProduct in cmd/idiom/product_workflow_test.go lines 1-80"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read TestCoverage in internal/productworkflow/coverage_gate_test.go lines 1-80"},
	}

	if answer, ok := planThenExecuteNoopSubmitPlanSourceSatisfiedAnswer(brief, req, result, results); ok {
		t.Fatalf("single no-op rejected edit must not close all required paths as source-satisfied, got:\n%s", answer)
	}
}

func TestPlanThenExecuteNoopSubmitPlanDoesNotSourceSatisfyMissingRequestedTestEdit(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
		Objective:  "reject temp symlinks and add regression tests proving no partial mutation",
	}
	req := workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{
			{Path: "internal/productworkflow/migration_apply.go", OldText: "same", NewText: "same", Purpose: "source"},
		},
	}
	result := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected",
		Error:   "edits[0]: old_text and new_text are identical; submit real edits, or answer `Decision: source-satisfied`",
	}

	if answer, ok := planThenExecuteNoopSubmitPlanSourceSatisfiedAnswer(brief, req, result, nil); ok {
		t.Fatalf("source-only no-op must not close a request that asked for tests, got:\n%s", answer)
	}
}

func TestReviewRepairCompletionGateRecoveryWithSourceSanityFailureAllowsBoundedEvidence(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
			Readiness: &workerruntime.ReadinessState{
				OpenObligations: []string{"source_sanity_failed:internal/productworkflow/coverage_gate.go"},
			},
		},
	}
	prompt, hideReads, ok := runner.completionGateRecoveryPrompt([]workspace.ToolResult{
		{
			Tool:    "completion_gate",
			Summary: "Review-repair completion gate rejected final answer",
			Error:   "review-repair must close with exactly one terminal outcome",
		},
	})
	if !ok {
		t.Fatal("review-repair completion rejection should produce a phase-specific recovery prompt")
	}
	if hideReads {
		t.Fatal("source_sanity repair should allow one bounded evidence read before terminal closeout")
	}
	for _, want := range []string{
		"source_sanity_failed:internal/productworkflow/coverage_gate.go",
		"one bounded source-read action",
		"existing type shape",
		"corrective `submit_plan`",
		"`Decision: pass` is invalid",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("review-repair source_sanity prompt missing %q:\n%s", want, prompt)
		}
	}

	_, hideReads, ok = runner.completionGateRecoveryPrompt([]workspace.ToolResult{
		{
			Tool:    "completion_gate",
			Summary: "Review-repair completion gate rejected final answer",
			Error:   "review-repair must close with exactly one terminal outcome",
		},
		{
			Tool:    "completion_gate",
			Summary: "Review-repair completion gate rejected final answer",
			Error:   "review-repair must close with exactly one terminal outcome",
		},
	})
	if !ok {
		t.Fatal("repeated review-repair completion rejection should still produce recovery")
	}
	if !hideReads {
		t.Fatal("repeated review-repair closeout rejection should return to terminal-only recovery")
	}
}

func TestPlanThenExecuteNoopSubmitPlanDoesNotSynthesizeFromReadTestEvidence(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}
	req := workspace.ToolRequest{
		Tool:      "submit_plan",
		Rationale: "The objective is to add regression tests proving no partial mutation; the existing test already covers it.",
		Edits: []workspace.PlanEdit{
			{Path: "internal/productworkflow/migration_apply.go", OldText: "same", NewText: "same", Purpose: "source"},
		},
	}
	result := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected",
		Error:   "edits[0]: old_text and new_text are identical; submit real edits, or answer `Decision: source-satisfied`",
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f5 internal/productworkflow/migration_apply_test.go lines 1-537"},
	}

	if answer, ok := planThenExecuteNoopSubmitPlanSourceSatisfiedAnswer(brief, req, result, results); ok {
		t.Fatalf("read test evidence must not turn a rejected no-op plan into success:\n%s", answer)
	}
}

func TestPlanThenExecuteNoopSubmitPlanDoesNotSynthesizeWhenObjectiveSummaryIsSparse(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}
	req := workspace.ToolRequest{
		Tool:      "submit_plan",
		Rationale: "source no-op",
		Edits: []workspace.PlanEdit{
			{Path: "internal/productworkflow/migration_apply.go", OldText: "same", NewText: "same", Purpose: "source"},
		},
	}
	result := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected",
		Error:   "edits[0]: old_text and new_text are identical; submit real edits, or answer `Decision: source-satisfied`",
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f5 internal/productworkflow/migration_apply_test.go lines 1-537"},
	}

	if answer, ok := planThenExecuteNoopSubmitPlanSourceSatisfiedAnswer(brief, req, result, results); ok {
		t.Fatalf("sparse objective no-op must not turn a rejected no-op plan into success:\n%s", answer)
	}
}

func TestPlanThenExecuteShortCircuitSkipsNonPlanExecuteLanes(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{TaskFamily: "implementation"}
	req := workspace.ToolRequest{Tool: "submit_plan"}
	result := workspace.ToolResult{Tool: "submit_plan", Summary: "ok"}
	if _, ok := planThenExecuteShortCircuitAnswer(brief, req, result); ok {
		t.Fatalf("short-circuit must only fire under plan_then_execute_planner; got ok")
	}
}

func TestPlanThenExecuteShortCircuitSkipsOtherTools(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{TaskFamily: PlanThenExecutePlannerTaskFamily}
	req := workspace.ToolRequest{Tool: "read_file"}
	result := workspace.ToolResult{Tool: "read_file", Summary: "Read f1 a.txt lines 1-10"}
	if _, ok := planThenExecuteShortCircuitAnswer(brief, req, result); ok {
		t.Fatalf("short-circuit must only fire on submit_plan; got ok for %q", req.Tool)
	}
}

func TestReviewRepairDoesNotAppendExternalTools(t *testing.T) {
	if runnerBriefAllowsExternalToolAppend(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
		PermissionProfile: "plan_submission_review",
	}) {
		t.Fatalf("review-repair must keep the exact phase tool surface and not append project externals")
	}
	if !runnerBriefAllowsExternalToolAppend(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		PermissionProfile: "guarded_workspace_write",
	}) {
		t.Fatalf("planner phase should still inherit project discovery externals")
	}
	if runnerBriefAllowsExternalToolAppend(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "verification_only",
		PermissionProfile: "read_only_verification_shell",
	}) {
		t.Fatalf("verification-only lanes must not widen their shell/check surface with project externals")
	}
}

func TestReviewRepairInitialTextualSurfaceAllowsTargetedProbeTools(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
		PermissionProfile: "plan_submission_review",
		AllowedTools:      []string{"read_file", "code_navigate", "context_status", "context_query", "context_zoom", "context_pack", "submit_plan"},
	}
	got := runnerPlanExecuteTextualAllowedTools([]string{"submit_plan"}, brief, nil)
	assertContains(t, got, "read_file")
	assertContains(t, got, "code_navigate")
	assertContains(t, got, "context_status")
	assertContains(t, got, "submit_plan")
	assertNotContains(t, got, "context_pack")

	terminal := runnerPlanExecuteTextualAllowedTools([]string{"submit_plan"}, brief, []workspace.ToolResult{{
		Tool:    "completion_gate",
		Summary: "Review-repair completion gate rejected final answer",
		Error:   "review-repair must close",
	}})
	assertEqualSet(t, terminal, []string{"submit_plan"})
}

func TestImplementationScopedEditAllowsSubmitPlan(t *testing.T) {
	// The scoped-edit tool surface filter trims the brief's
	// allowed_tools to write tools + read_file + code_navigate. Without
	// an explicit case, submit_plan is filtered out (it isn't in
	// IsWorkspaceWriteTool's list because it doesn't directly mutate
	// files), which leaves the planner phase with only read tools and
	// no way to submit a plan. The first three plan-execute Colony runs
	// failed for a chain of related reasons, this filter being the
	// last layer.
	if !implementationScopedEditToolAllowed("submit_plan") {
		t.Fatalf("scoped-edit surface must allow submit_plan; otherwise plan-execute lanes lose their write tool")
	}
	if !implementationPreWriteToolAllowed("submit_plan") {
		t.Fatalf("pre-write surface must allow submit_plan; otherwise exact-anchor pre-write hides the planner's write")
	}
	for _, tool := range []string{"edit_file", "apply_patch", "read_file", "code_navigate", "submit_plan", "context_query", "cpp_find_symbol", "cpp_definition", "source_sanity"} {
		got := implementationScopedEditAllowedToolsWithRegistry([]string{tool}, nil)
		if len(got) != 1 || got[0] != tool {
			t.Fatalf("scoped-edit allowlist should pass %q through, got %v", tool, got)
		}
	}
	for _, tool := range []string{"shell", "git_status", "task", "search_content", "list_files", "context_index"} {
		got := implementationScopedEditAllowedToolsWithRegistry([]string{tool}, nil)
		if len(got) != 0 {
			t.Fatalf("scoped-edit allowlist should drop %q, got %v", tool, got)
		}
	}

	reg, err := workspace.NewExternalToolRegistry([]workspace.ExternalTool{
		{Name: "trace_code", Command: "true", ReadOnly: true, Discovery: true},
		{Name: "queue_run", Command: "true", ReadOnly: false, Discovery: false},
	})
	if err != nil {
		t.Fatalf("NewExternalToolRegistry: %v", err)
	}
	got := implementationScopedEditAllowedToolsWithRegistry([]string{"trace_code", "queue_run", "edit_file"}, reg)
	want := []string{"trace_code", "edit_file"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("scoped-edit allowlist should keep declared read-only discovery externals only\nwant %v\n got %v", want, got)
	}
}

func TestPlanExecuteSubmitPlanCountsAsWriteDuringWriteOnlyRecovery(t *testing.T) {
	r := &Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{TaskFamily: PlanThenExecutePlannerTaskFamily}}
	if !r.writeOnlyRecoveryToolIsWrite("submit_plan") {
		t.Fatal("plan-execute write-only recovery must treat submit_plan as the planner write contract")
	}
	reviewRepair := &Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{TaskFamily: PlanThenExecuteReviewRepairTaskFamily}}
	if !reviewRepair.writeOnlyRecoveryToolIsWrite("submit_plan") {
		t.Fatal("review-repair terminal recovery must treat submit_plan as the corrective-plan contract")
	}
	normal := &Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{TaskFamily: "implementation"}}
	if normal.writeOnlyRecoveryToolIsWrite("submit_plan") {
		t.Fatal("submit_plan should not be treated as a normal implementation write tool outside plan-execute planner phase")
	}
}

func TestPlanExecuteRecoveryRejectsNonSubmitPlanWithoutTerminalWriteOnlyBlocker(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte("before\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	replies := []string{
		`{"tool":"read_file","path":"a.txt"}`,
		`{"tool":"read_file","path":"b.txt"}`,
		"Done.",
		`{"tool":"search_content","path":".","pattern":"before"}`,
		"<<QUESTION>>: exact blocker: no safe edit body is available for a.txt or b.txt.",
	}
	var requests int
	var sawSubmitPlanRequired bool
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
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 6},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "plan_then_execute_submit_plan_required" {
				sawSubmitPlanRequired = true
			}
			if event.Role == "completion_status" && strings.Contains(event.Content, "write_only_recovery_required_paths_remaining") {
				t.Fatalf("plan-execute recovery should not terminate on first non-submit_plan tool: %s", event.Content)
			}
		},
	}
	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan edits"}}, llama.ChatOptions{MaxTokens: 2048}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if requests != len(replies) {
		t.Fatalf("expected recovery to continue after non-submit_plan tool, got %d request(s)", requests)
	}
	if !sawSubmitPlanRequired {
		t.Fatalf("expected plan_then_execute_submit_plan_required event")
	}
	if !strings.Contains(answer, "exact blocker") {
		t.Fatalf("expected final blocker answer after recovery, got:\n%s", answer)
	}
}

func TestPlanExecuteRecoveryForcesSubmitPlanOnlyAfterRejectedPlan(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte("before\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	replies := []string{
		`{"tool":"read_file","path":"a.txt"}`,
		`{"tool":"read_file","path":"b.txt"}`,
		`{"tool":"submit_plan","rationale":"Update only one path so the required-scope gate rejects it.","edits":[{"path":"a.txt","old_text":"before\n","new_text":"after\n","purpose":"replace a fixture"}]}`,
		`{"tool":"submit_plan","rationale":"Update every required path after the required-scope gate rejected the incomplete plan.","edits":[{"path":"a.txt","old_text":"before\n","new_text":"after\n","purpose":"replace a fixture"},{"path":"b.txt","old_text":"before\n","new_text":"after\n","purpose":"replace b fixture"}]}`,
	}
	type requestPayload struct {
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
		ToolChoice string `json:"tool_choice"`
	}
	var requests int
	var recoveryPayload requestPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}
		if requests == 3 {
			if err := json.Unmarshal(body, &recoveryPayload); err != nil {
				t.Fatalf("decode recovery request: %v\n%s", err, string(body))
			}
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
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}
	var toolTrace []string
	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan edits"}}, llama.ChatOptions{
		MaxTokens: 2048,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "search_content"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "auto",
	}, nil, func(event ToolRequestEvent) {
		toolTrace = append(toolTrace, "request "+event.Request.Tool+": "+event.Raw)
	}, func(event ToolResultEvent) {
		toolTrace = append(toolTrace, "result "+event.Request.Tool+": "+event.Result.Summary+" | "+event.Result.Error)
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(recoveryPayload.Tools) != 1 || recoveryPayload.Tools[0].Function.Name != "submit_plan" {
		t.Fatalf("after rejected submit_plan, recovery request should expose only submit_plan, got %#v", recoveryPayload.Tools)
	}
	if recoveryPayload.ToolChoice != "required" {
		t.Fatalf("after rejected submit_plan, recovery request should require submit_plan, got %q", recoveryPayload.ToolChoice)
	}
	if !strings.Contains(answer, "Plan submitted with 2 edit") {
		t.Fatalf("expected successful corrected plan summary, got:\n%s", answer)
	}
}

func TestPlanExecutePartialNoopSubmitPlanFallsThroughToCorrection(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte("before\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	replies := []string{
		`{"tool":"read_file","path":"a.txt"}`,
		`{"tool":"read_file","path":"b.txt"}`,
		`{"tool":"submit_plan","rationale":"Update one file but accidentally include a no-op for the second required path.","edits":[{"path":"a.txt","old_text":"before\n","new_text":"after a\n","purpose":"replace a fixture"},{"path":"b.txt","old_text":"before\n","new_text":"before\n","purpose":"replace b fixture"}]}`,
		`{"tool":"submit_plan","rationale":"Correct the rejected no-op so every required path has a real edit.","edits":[{"path":"a.txt","old_text":"before\n","new_text":"after a\n","purpose":"replace a fixture"},{"path":"b.txt","old_text":"before\n","new_text":"after b\n","purpose":"replace b fixture"}]}`,
	}
	var requests int
	var sawSubmitPlanRequired bool
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
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "plan_then_execute_source_satisfied_short_circuit" {
				t.Fatalf("partial no-op submit_plan must not source-satisfy every required path: %s", event.Content)
			}
			if event.Role == "plan_then_execute_submit_plan_required" {
				sawSubmitPlanRequired = true
			}
		},
	}
	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan edits"}}, llama.ChatOptions{
		MaxTokens: 2048,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "auto",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if requests != len(replies) {
		t.Fatalf("expected recovery to request a corrected submit_plan, got %d request(s)", requests)
	}
	if !sawSubmitPlanRequired {
		t.Fatalf("expected plan_then_execute_submit_plan_required event")
	}
	if !strings.Contains(answer, "Plan submitted with 2 edit") {
		t.Fatalf("expected successful corrected plan summary, got:\n%s", answer)
	}
}

func TestPlanExecuteCandidateRejectedPlanDoesNotAcceptGenericBlockedProse(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}
	replies := []string{
		`{"tool":"read_file","path":"a.txt"}`,
		`{"tool":"submit_plan","rationale":"Use a stale candidate anchor so the plan is rejected.","edits":[{"path":"a.txt","old_text":"missing\n","new_text":"after\n","purpose":"replace a fixture"}]}`,
		"The planner is blocked because candidate source is already ready but no plan was submitted.",
		"<<QUESTION>>: exact blocker: current source anchor for a.txt could not be recovered after the rejected submit_plan.",
	}
	var requests int
	var sawCompletionRejection bool
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
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedEditPaths:  []string{"a.txt"},
		},
	}
	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan candidate edit"}}, llama.ChatOptions{
		MaxTokens: 2048,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "auto",
	}, nil, nil, func(event ToolResultEvent) {
		if event.Result.Tool == "completion_gate" && strings.Contains(event.Result.Summary, "Plan-execute completion gate rejected final answer") {
			sawCompletionRejection = true
		}
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if requests != len(replies) {
		t.Fatalf("generic blocked prose should have been rejected and retried, got %d request(s)", requests)
	}
	if !sawCompletionRejection {
		t.Fatal("expected completion gate rejection for generic blocked prose")
	}
	if !strings.Contains(answer, "exact blocker") {
		t.Fatalf("expected final typed blocker, got:\n%s", answer)
	}
}

func TestPlanExecuteLargeInsertAfterLineRecoveryNamesLineRangeAlternative(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte("before\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	large := strings.Repeat("inserted line\n", 360)
	replies := []string{
		`{"tool":"read_file","path":"a.txt"}`,
		`{"tool":"read_file","path":"b.txt"}`,
		fmt.Sprintf(`{"tool":"submit_plan","rationale":"Use an oversized insertion so schema recovery must name line-range replacement.","edits":[{"path":"a.txt","insert_after_line":1,"content":%q,"purpose":"insert a large block"},{"path":"b.txt","old_text":"before\n","new_text":"after\n","purpose":"replace b fixture"}]}`, large),
		fmt.Sprintf(`{"tool":"submit_plan","rationale":"Correct large insertion with line-range replacement.","edits":[{"path":"a.txt","start_line":1,"end_line":1,"content":%q,"purpose":"replace anchor with anchor plus inserted block"},{"path":"b.txt","old_text":"before\n","new_text":"after\n","purpose":"replace b fixture"}]}`, "before\n"+large),
	}
	type requestPayload struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
		ToolChoice string `json:"tool_choice"`
	}
	var requests int
	var recoveryPayload requestPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}
		if requests == 3 {
			if err := json.Unmarshal(body, &recoveryPayload); err != nil {
				t.Fatalf("decode recovery request: %v\n%s", err, string(body))
			}
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
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}
	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan edits"}}, llama.ChatOptions{
		MaxTokens: 2048,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "auto",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(recoveryPayload.Tools) != 1 || recoveryPayload.Tools[0].Function.Name != "submit_plan" {
		t.Fatalf("after large insert rejection, recovery request should expose only submit_plan, got %#v", recoveryPayload.Tools)
	}
	if recoveryPayload.ToolChoice != "required" {
		t.Fatalf("after large insert rejection, recovery request should require submit_plan, got %q", recoveryPayload.ToolChoice)
	}
	var correction string
	for _, message := range recoveryPayload.Messages {
		if strings.Contains(message.Content, "do not repeat `insert_after_line`") ||
			strings.Contains(message.Content, "Do not repeat `insert_after_line`") {
			correction = message.Content
			break
		}
	}
	if correction == "" {
		t.Fatalf("large insertion recovery prompt missing from request messages: %#v", recoveryPayload.Messages)
	}
	for _, want := range []string{"do not repeat `insert_after_line`", "start_line", "end_line", "unchanged anchor line"} {
		if !strings.Contains(correction, want) {
			t.Fatalf("large insertion recovery prompt missing %q:\n%s", want, correction)
		}
	}
	if strings.Contains(correction, `"insert_after_line":42`) {
		t.Fatalf("large insertion recovery should not render generic insertion example:\n%s", correction)
	}
	if !strings.Contains(answer, "Plan submitted with 2 edit") {
		t.Fatalf("expected successful corrected plan summary, got:\n%s", answer)
	}
}

func TestPlanExecuteRecoveryForcesAnchorRefreshAfterInvalidSubmitPlan(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte("before\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	replies := []string{
		`{"tool":"read_file","path":"a.txt"}`,
		`{"tool":"read_file","path":"b.txt"}`,
		`{"tool":"submit_plan","rationale":"Use a stale anchor so recovery must refresh source before another plan.","edits":[{"path":"a.txt","old_text":"missing\n","new_text":"after\n","purpose":"replace a fixture"},{"path":"b.txt","old_text":"before\n","new_text":"after\n","purpose":"replace b fixture"}]}`,
		`{"tool":"read_file","path":"a.txt","start_line":1,"end_line":1}`,
		`{"tool":"submit_plan","rationale":"Update every required path after refreshing the stale anchor.","edits":[{"path":"a.txt","old_text":"before\n","new_text":"after\n","purpose":"replace a fixture"},{"path":"b.txt","old_text":"before\n","new_text":"after\n","purpose":"replace b fixture"}]}`,
	}
	type requestPayload struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
		ToolChoice string `json:"tool_choice"`
	}
	var requests int
	var recoveryPayload requestPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}
		if requests == 3 {
			if err := json.Unmarshal(body, &recoveryPayload); err != nil {
				t.Fatalf("decode anchor recovery request: %v\n%s", err, string(body))
			}
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
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}
	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan edits"}}, llama.ChatOptions{
		MaxTokens: 2048,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "code_navigate"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "auto",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if recoveryPayload.ToolChoice != "required" {
		t.Fatalf("anchor recovery request should require a parser-readable source refresh, got %q", recoveryPayload.ToolChoice)
	}
	var exposed []string
	for _, tool := range recoveryPayload.Tools {
		exposed = append(exposed, tool.Function.Name)
	}
	slices.Sort(exposed)
	if !slices.Equal(exposed, []string{"code_navigate", "read_file"}) {
		t.Fatalf("anchor recovery should expose only source refresh tools, got %#v", recoveryPayload.Tools)
	}
	var recoveryText strings.Builder
	for _, message := range recoveryPayload.Messages {
		recoveryText.WriteString(message.Role)
		recoveryText.WriteString(": ")
		recoveryText.WriteString(message.Content)
		recoveryText.WriteString("\n")
	}
	for _, want := range []string{"anchor-refresh phase", "bounded source-body request for one of those paths", "Do not call `submit_plan` again until the bounded refresh returns"} {
		if !strings.Contains(recoveryText.String(), want) {
			t.Fatalf("anchor recovery request missing %q:\n%s", want, recoveryText.String())
		}
	}
	if strings.Contains(recoveryText.String(), "The next visible action must be a corrected parser-readable `submit_plan` request") {
		t.Fatalf("anchor recovery request must not also demand corrected submit_plan before refresh:\n%s", recoveryText.String())
	}
	if !strings.Contains(answer, "Plan submitted with 2 edit") {
		t.Fatalf("expected successful corrected plan summary, got:\n%s", answer)
	}
}

func TestPlanExecuteRepairsStringifiedSubmitPlanEditsBeforeValidation(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte("before\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	replies := []string{
		`{"tool":"read_file","path":"a.txt"}`,
		`{"tool":"read_file","path":"b.txt"}`,
		`{"tool":"submit_plan","rationale":"Bad schema: edits is stringified JSON.","edits":"[{\"path\":\"a.txt\",\"old_text\":\"before\\n\",\"new_text\":\"after\\n\",\"purpose\":\"replace a fixture\"},{\"path\":\"b.txt\",\"old_text\":\"before\\n\",\"new_text\":\"after\\n\",\"purpose\":\"replace b fixture\"}]"}`,
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
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}
	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan edits"}}, llama.ChatOptions{
		MaxTokens: 2048,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "search_content"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "auto",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if requests != len(replies) {
		t.Fatalf("valid stringified edits should be repaired without another model request, got %d request(s)", requests)
	}
	if !strings.Contains(answer, "Plan submitted with 2 edit") {
		t.Fatalf("expected repaired plan summary, got:\n%s", answer)
	}
}

func TestPlanExecuteParseRecoveryForcesSubmitPlanOnlyAfterUnrepairableStringifiedEdits(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte("before\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	replies := []string{
		`{"tool":"read_file","path":"a.txt"}`,
		`{"tool":"read_file","path":"b.txt"}`,
		`{"tool":"submit_plan","rationale":"Bad schema: edits is a non-JSON string.","edits":"not json"}`,
		`{"tool":"submit_plan","rationale":"Corrected schema after parse recovery.","edits":[{"path":"a.txt","old_text":"before\n","new_text":"after\n","purpose":"replace a fixture"},{"path":"b.txt","old_text":"before\n","new_text":"after\n","purpose":"replace b fixture"}]}`,
	}
	type requestPayload struct {
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
		ToolChoice         string `json:"tool_choice"`
		ChatTemplateKwargs *struct {
			EnableThinking *bool `json:"enable_thinking"`
		} `json:"chat_template_kwargs,omitempty"`
	}
	var requests int
	var recoveryPayload requestPayload
	var sawParseRecovery bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}
		if requests == 3 {
			if err := json.Unmarshal(body, &recoveryPayload); err != nil {
				t.Fatalf("decode recovery request: %v\n%s", err, string(body))
			}
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
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "plan_then_execute_submit_plan_parse_recovery" {
				sawParseRecovery = true
				if !strings.Contains(event.Content, "Do not call read/search/discovery") {
					t.Fatalf("parse recovery prompt should block renewed discovery, got:\n%s", event.Content)
				}
			}
		},
	}
	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan edits"}}, llama.ChatOptions{
		MaxTokens: 2048,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "search_content"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "auto",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !sawParseRecovery {
		t.Fatalf("expected plan_then_execute_submit_plan_parse_recovery event")
	}
	if len(recoveryPayload.Tools) != 1 || recoveryPayload.Tools[0].Function.Name != "submit_plan" {
		t.Fatalf("after unrepairable stringified-edits parse failure, recovery request should expose only submit_plan, got %#v", recoveryPayload.Tools)
	}
	if recoveryPayload.ToolChoice != "required" {
		t.Fatalf("after unrepairable stringified-edits parse failure, recovery request should require submit_plan, got %q", recoveryPayload.ToolChoice)
	}
	if recoveryPayload.ChatTemplateKwargs == nil || recoveryPayload.ChatTemplateKwargs.EnableThinking == nil || *recoveryPayload.ChatTemplateKwargs.EnableThinking {
		t.Fatalf("parse recovery should force visible submit_plan output over hidden thinking, got %#v", recoveryPayload.ChatTemplateKwargs)
	}
	if !strings.Contains(answer, "Plan submitted with 2 edit") {
		t.Fatalf("expected successful corrected plan summary, got:\n%s", answer)
	}
}

func TestPlanExecuteSyntaxRejectedSubmitPlanAllowsRefreshThenCorrectedPlan(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package main\n\nfunc value() int {\n\treturn 1\n}\n"), 0o644); err != nil {
		t.Fatalf("write a.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatalf("write b.txt: %v", err)
	}
	replies := []string{
		`{"tool":"read_file","path":"a.go"}`,
		`{"tool":"read_file","path":"b.txt"}`,
		`{"tool":"submit_plan","rationale":"Bad line-range edit should fail syntax preflight.","edits":[{"path":"a.go","start_line":3,"end_line":3,"content":"func broken {\n","purpose":"bad source edit"},{"path":"b.txt","old_text":"before\n","new_text":"after\n","purpose":"replace b"}]}`,
		`{"tool":"read_file","path":"a.go","start_line":1,"end_line":5}`,
		`{"tool":"submit_plan","rationale":"Corrected after source refresh.","edits":[{"path":"a.go","old_text":"\treturn 1\n","new_text":"\treturn 2\n","purpose":"replace return value"},{"path":"b.txt","old_text":"before\n","new_text":"after\n","purpose":"replace b"}]}`,
	}
	type requestPayload struct {
		Tools      []planExecuteRequestToolPayload `json:"tools"`
		ToolChoice string                          `json:"tool_choice"`
	}
	var requests int
	var extraRequests int
	var refreshPayload requestPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			extraRequests++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{
					"message": map[string]string{"content": "<<QUESTION>>: unexpected extra request after corrected plan."},
				}},
			})
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}
		if requests == 3 {
			if err := json.Unmarshal(body, &refreshPayload); err != nil {
				t.Fatalf("decode refresh request: %v\n%s", err, string(body))
			}
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

	var events []string
	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 10, MaxNoProgressTurns: 10},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go", "b.txt"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "tool_result" || strings.Contains(event.Role, "plan_then_execute") || strings.Contains(event.Role, "submit_plan") || strings.Contains(event.Role, "completion") {
				events = append(events, event.Role+": "+event.Content)
			}
		},
	}
	var toolTrace []string
	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan edits"}}, llama.ChatOptions{
		MaxTokens: 2048,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "search_content"}},
			{Type: "function", Function: llama.ToolFunction{Name: "code_navigate"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "auto",
	}, nil, func(event ToolRequestEvent) {
		toolTrace = append(toolTrace, "request "+event.Request.Tool+": "+event.Raw)
	}, func(event ToolResultEvent) {
		toolTrace = append(toolTrace, "result "+event.Request.Tool+": "+event.Result.Summary+" | "+event.Result.Error)
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(refreshPayload.Tools) != 2 || !requestPayloadExposesTool(refreshPayload.Tools, "read_file") || !requestPayloadExposesTool(refreshPayload.Tools, "code_navigate") || requestPayloadExposesTool(refreshPayload.Tools, "submit_plan") {
		t.Fatalf("syntax preflight recovery should expose only bounded source-refresh tools, got %#v", refreshPayload.Tools)
	}
	if refreshPayload.ToolChoice != "required" {
		t.Fatalf("syntax preflight recovery should require a tool call, got %q", refreshPayload.ToolChoice)
	}
	if extraRequests != 0 {
		t.Fatalf("unexpected extra chat request after corrected plan; tools:\n%s\nevents:\n%s\nanswer:\n%s", strings.Join(toolTrace, "\n"), strings.Join(events, "\n"), answer)
	}
	if !strings.Contains(answer, "Plan submitted with 2 edit") {
		t.Fatalf("expected successful corrected plan summary, got:\n%s\ntools:\n%s\nevents:\n%s", answer, strings.Join(toolTrace, "\n"), strings.Join(events, "\n"))
	}
}

type planExecuteRequestToolPayload struct {
	Function struct {
		Name string `json:"name"`
	} `json:"function"`
}

func requestPayloadExposesTool(tools []planExecuteRequestToolPayload, name string) bool {
	for _, tool := range tools {
		if tool.Function.Name == name {
			return true
		}
	}
	return false
}

func TestPlanExecuteRecoveryRetriesAfterNonSpecificSubmitPlanBlocker(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte("before\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	replies := []string{
		`{"tool":"read_file","path":"a.txt"}`,
		`{"tool":"read_file","path":"b.txt"}`,
		"Done.",
		`{"tool":"read_file","path":"a.txt"}`,
		"Still done.",
		"<<QUESTION>>: blocked by missing safe edit body.\n- a.txt: no safe edit body is available.\n- b.txt: no safe edit body is available.",
	}
	var requests int
	var recoveryPrompts int
	var sawRepeatedSubmitPlanMissingBlocker bool
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
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
		OnEvent: func(event RunEvent) {
			switch event.Role {
			case "completion_gate_recovery":
				recoveryPrompts++
			case "completion_status":
				if strings.Contains(event.Content, "completion_gate_submit_plan_missing_repeated") {
					sawRepeatedSubmitPlanMissingBlocker = true
				}
			}
		},
	}
	answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "plan edits"}}, llama.ChatOptions{MaxTokens: 2048}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if requests >= len(replies) {
		t.Fatalf("expected recovery to cut over before exhausting scripted retries, got %d request(s)", requests)
	}
	if recoveryPrompts < 1 {
		t.Fatalf("expected one completion recovery prompt before cutover, got %d", recoveryPrompts)
	}
	if !sawRepeatedSubmitPlanMissingBlocker {
		t.Fatalf("expected repeated submit-plan-missing completion status")
	}
	if !strings.Contains(answer, "required edit path") {
		t.Fatalf("expected deterministic required-path blocker answer after recovery cutover, got:\n%s", answer)
	}
}

func TestPlanExecuteCompletionBlockerKindDistinguishesInvalidSubmitPlan(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.txt lines 1-2 (total lines: 2)", Output: "before a\n"},
		{Tool: "read_file", Summary: "Read f2 b.txt lines 1-2 (total lines: 2)", Output: "before b\n"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "submit_plan must include at least one edit entry for every required_edit_paths entry",
		},
	}
	if got := runner.completionGateRepeatedBlockerKind(results); got != "completion_gate_submit_plan_invalid_repeated" {
		t.Fatalf("completion blocker kind = %q, want submit-plan invalid", got)
	}
}

func TestImplementationScopedSurfaceActiveForRequiredPathMutation(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{".idiom/refine/example/review.md"},
	}
	if !implementationScopedEditSurfaceActive(brief, nil) {
		t.Fatalf("required-path mutation packets should use scoped discovery/edit tools even without target-packet anchors")
	}
	brief.RequiredEditPaths = nil
	if implementationScopedEditSurfaceActive(brief, nil) {
		t.Fatalf("scoped implementation surface must stay inactive without required edit paths")
	}
}

func TestScopedImplementationDiscoveryLookupAllowsSemanticAndDeclaredProjectTools(t *testing.T) {
	reg, err := workspace.NewExternalToolRegistry([]workspace.ExternalTool{
		{Name: "lookup", Command: "true", ReadOnly: true, Discovery: true},
		{Name: "queue_run", Command: "true", ReadOnly: false, Discovery: false},
	})
	if err != nil {
		t.Fatalf("NewExternalToolRegistry: %v", err)
	}
	root := t.TempDir()
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root, EnableWrites: true}),
	}
	runner.Workspace.SetExternalToolsRegistry(reg)

	allowed := []workspace.ToolRequest{
		{Tool: "cpp_find_symbol", Pattern: "FormativeEventType"},
		{Tool: "context_query", Query: "FormativeEventType owner", Resolution: "symbol"},
		{Tool: "lookup", Query: "FormativeEventType"},
		{Tool: "code_navigate", Capability: "file_outline", Path: "native/src/simulation/creatures/formative_events.h"},
		{Tool: "code_navigate", Capability: "read_symbol", Path: "native/src/simulation/creatures", Pattern: "ValidateFormativeEvent"},
		{Tool: "discovery_query", Purpose: "trace_code", Path: "native/src/simulation/creatures", Pattern: "ValidateFormativeEvent"},
		{Tool: "structural_search", Path: "native/src/simulation/creatures", Pattern: "ValidateFormativeEvent", Language: "cpp"},
	}
	for _, req := range allowed {
		if !runner.scopedImplementationDiscoveryLookupAllowed(req, []string{"native/src/simulation/creatures/formative_events.h"}) {
			t.Fatalf("expected scoped discovery lookup to allow %#v", req)
		}
	}
	rejected := []workspace.ToolRequest{
		{Tool: "search_content", Pattern: "FormativeEventType"},
		{Tool: "queue_run", Query: "FormativeEventType"},
		{Tool: "semgrep_scan", Path: "."},
		{Tool: "cpp_find_symbol", Pattern: "*"},
		{Tool: "code_navigate", Capability: "file_outline", Path: "native/src/core/components.h"},
		{Tool: "structural_search", Pattern: "enum class FormativeEventType", Path: "native/src"},
		{Tool: "structural_search", Pattern: "if $COND { $BODY }", Path: "native/src/simulation/creatures", Language: "cpp"},
		{Tool: "code_navigate", Capability: "read_symbol", Path: "native/src/simulation", Pattern: "ValidateFormativeEvent"},
		{Tool: "code_navigate", Capability: "read_symbol", Path: "native/src/simulation/core", Pattern: "ValidateFormativeEvent"},
	}
	for _, req := range rejected {
		if runner.scopedImplementationDiscoveryLookupAllowed(req, []string{"native/src/simulation/creatures/formative_events.h"}) {
			t.Fatalf("expected scoped discovery lookup to reject %#v", req)
		}
	}
}

func TestDocDeliverableRecoveryNudgesScopeCorrectionAfterOutOfScopeRead(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{".idiom/refine/example/iter-2/draft.md"},
			EvidencePaths:     []string{"native/src/simulation/creatures/formative_events.h"},
		},
	}
	req := workspace.ToolRequest{
		Tool:    "structural_search",
		Path:    "native/src",
		Pattern: "enum class FormativeEventType",
	}
	_, _, prompt, hideReads, rejected := runner.writeReadyPacketReadWithoutGapRecovery(req, nil)
	if !rejected {
		t.Fatalf("expected out-of-scope read to be rejected")
	}
	if hideReads {
		t.Fatalf("doc deliverable out-of-scope recovery should not activate write-only read hiding")
	}
	for _, want := range []string{"Treat this as scope evidence", "scope-correction", ".idiom/refine/example/iter-2/draft.md"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to contain %q, got %q", want, prompt)
		}
	}
}

func TestDocDeliverableCompletionRecoveryRecordsEvidenceGapWithoutHidingReads(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{".idiom/refine/example/iter-2/draft.md"},
		},
	}
	prompt, hideReads, ok := runner.completionGateRecoveryPrompt(nil)
	if !ok {
		t.Fatalf("expected completion recovery prompt")
	}
	if hideReads {
		t.Fatalf("doc deliverable completion recovery should not hide reads")
	}
	for _, want := range []string{"non-source deliverable", "deliverable-write contract", "Source verification pending", "<<QUESTION>>"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to contain %q, got %q", want, prompt)
		}
	}
}

func TestDocDeliverableCompletionRecoveryStateClosesTowardWriteOrBlocker(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"read_file", "write_file", "code_navigate"},
		RequiredEditPaths: []string{".idiom/refine/example/iter-2/draft.md"},
		EvidencePaths:     []string{".idiom/refine/example/iter-1/draft.md"},
	}
	result := workspace.ToolResult{
		Tool:    "completion_gate",
		Summary: "Mutation completion gate rejected final answer",
		Error:   "this task asks for a workspace change, but no successful write tool result is present",
	}
	state := buildRunnerNextActionState("main", 3, "completion_gate_rejected", workspace.ToolRequest{Tool: "completion_gate"}, result, []workspace.ToolResult{result}, false, brief)

	joinedValid := strings.Join(state.NextValidActions, "\n")
	for _, want := range []string{"deliverable-write capability", ".idiom/refine/example/iter-2/draft.md", "<<QUESTION>>"} {
		if !strings.Contains(joinedValid, want) {
			t.Fatalf("expected next valid actions to close toward %q, got %#v", want, state.NextValidActions)
		}
	}
	if strings.Contains(joinedValid, "code_navigate") || strings.Contains(joinedValid, "read_file") {
		t.Fatalf("post-gate scratchpad closeout should not advertise new discovery as a normal next action: %#v", state.NextValidActions)
	}
	joinedInvalid := strings.Join(state.InvalidNextActions, "\n")
	if !strings.Contains(joinedInvalid, "new source discovery") {
		t.Fatalf("expected invalid actions to call out discovery after prose-only completion, got %#v", state.InvalidNextActions)
	}
	joinedEvidence := strings.Join(state.EvidenceStatus, "\n")
	if !strings.Contains(joinedEvidence, "non-source deliverable still unwritten") {
		t.Fatalf("expected evidence status to carry closeout state, got %#v", state.EvidenceStatus)
	}
}

func TestScratchpadDeliverableCloseoutRejectsNonWriteTools(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{".idiom/refine/example/iter-2/draft.md"},
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "completion_gate",
		Summary: "Mutation completion gate rejected final answer",
		Error:   "this task asks for a workspace change, but no successful write tool result is present",
	}}
	summary, errorText, prompt, target, reject := runner.scratchpadDeliverableCloseoutNonWriteRecovery(
		workspace.ToolRequest{Tool: "read_file", Path: "native/src/example.cpp"},
		results,
	)
	if !reject {
		t.Fatalf("post-completion scratchpad closeout should reject non-write tools")
	}
	joined := summary + " " + errorText + " " + prompt + " " + target
	for _, want := range []string{"Scratchpad deliverable closeout", "read_file", ".idiom/refine/example/iter-2/draft.md", "deliverable-write capability", "Source verification pending", "<<QUESTION>>"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected recovery text to contain %q, got summary=%q error=%q prompt=%q target=%q", want, summary, errorText, prompt, target)
		}
	}
	if _, _, _, _, reject := runner.scratchpadDeliverableCloseoutNonWriteRecovery(
		workspace.ToolRequest{Tool: "write_file", Path: ".idiom/refine/example/iter-2/draft.md"},
		results,
	); reject {
		t.Fatalf("write_file must remain available for scratchpad closeout")
	}
}

func TestScratchpadDeliverablePrewriteRejectsSourceDiscoveryButAllowsScratchpadReads(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{".idiom/refine/example/iter-2/draft.md"},
			EvidencePaths: []string{
				".idiom/refine/example/iter-1/draft.md",
				".idiom/refine/example/iter-1/review.md",
				"native/src/simulation/creatures/formative_events.h",
			},
		},
	}

	summary, errorText, prompt, reject := runner.scratchpadDeliverableSourceDiscoveryRecovery(
		workspace.ToolRequest{Tool: "read_file", Path: "native/src/simulation/creatures/formative_events.h"},
		nil,
	)
	if !reject {
		t.Fatalf("expected source read to be redirected to scratchpad write")
	}
	joined := summary + " " + errorText + " " + prompt
	for _, want := range []string{"Scratchpad deliverable source discovery", "deliverable-write contract", "Source verification pending", ".idiom/refine/example/iter-2/draft.md"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected recovery text to contain %q, got %q", want, joined)
		}
	}

	if _, _, _, reject := runner.scratchpadDeliverableSourceDiscoveryRecovery(
		workspace.ToolRequest{Tool: "context_query", Query: "FormativeEventType"},
		nil,
	); !reject {
		t.Fatalf("expected broad/pathless discovery to be redirected")
	}
	if _, _, _, reject := runner.scratchpadDeliverableSourceDiscoveryRecovery(
		workspace.ToolRequest{Tool: "read_file", Path: ".idiom/refine/example/iter-1/review.md"},
		nil,
	); reject {
		t.Fatalf("scratchpad input reads should remain available")
	}
	if _, _, _, reject := runner.scratchpadDeliverableSourceDiscoveryRecovery(
		workspace.ToolRequest{Tool: "write_file", Path: ".idiom/refine/example/iter-2/draft.md"},
		nil,
	); reject {
		t.Fatalf("write_file must remain available for scratchpad deliverables")
	}
}

func TestScopedImplementationReadScopeDoesNotExpandFromDynamicEvidence(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{".idiom/refine/example/iter-2/draft.md"},
			EvidencePaths:     []string{"native/src/simulation/creatures/formative_events.h"},
		},
	}
	req := workspace.ToolRequest{
		Tool:     "read_file",
		Path:     "native/src/core/components.h",
		NamedGap: "context query mentioned this candidate path",
	}
	results := []workspace.ToolResult{{
		Tool:   "context_query",
		Output: "candidate: native/src/core/components.h:42 AppraisalResult",
	}}
	_, _, prompt, _, rejected := runner.writeReadyPacketReadWithoutGapRecovery(req, results)
	if !rejected {
		t.Fatalf("dynamic tool-result evidence must not expand scratchpad read scope")
	}
	if !strings.Contains(prompt, "scope evidence") {
		t.Fatalf("expected scope evidence prompt, got %q", prompt)
	}
}

func TestScratchpadDeliverableFailedEvidenceNudgesDraftedGap(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{".idiom/refine/example/iter-2/draft.md"},
			EvidencePaths:     []string{".idiom/refine/example/iter-1/draft.md"},
		},
	}
	prompt, ok := runner.scratchpadDeliverableFailedEvidenceNudge(
		workspace.ToolRequest{Tool: "code_navigate", Capability: "references", Path: "native/src/example.cpp", Pattern: "MISSING_SYMBOL"},
		workspace.ToolResult{Tool: "code_navigate", Summary: "Code navigation routed references", Error: "no C++ symbols matched"},
		[]workspace.ToolResult{{Tool: "code_navigate", Summary: "Code navigation routed references", Error: "no C++ symbols matched"}},
	)
	if !ok {
		t.Fatalf("expected failed optional evidence to nudge scratchpad gap recording")
	}
	for _, want := range []string{"treat that as an evidence fact", "rather than repairing the lookup", "next action should be either writing", ".idiom/refine/example/iter-2/draft.md"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to contain %q, got %q", want, prompt)
		}
	}
}

func TestScratchpadDeliverableFailureFeedbackNarrowsToWriteOrQuestion(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"read_file", "write_file"},
		RequiredEditPaths: []string{".idiom/refine/example/iter-2/draft.md"},
		EvidencePaths:     []string{".idiom/refine/example/iter-1/draft.md"},
	}
	req := workspace.ToolRequest{
		Tool:    "read_file",
		Path:    "native/src/core/components.h",
		Pattern: "AppraisalResult",
	}
	result := workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Scoped implementation read rejected",
		Error:   "outside the required edit paths and explicit evidence paths",
	}
	packet := buildRunnerFailureFeedbackPacket("main", 3, "tool_rejected", req, result, []workspace.ToolResult{result}, false, brief)
	joinedRequired := strings.Join(packet.RequiredNext, "\n")
	if !strings.Contains(joinedRequired, "record the failed") {
		t.Fatalf("expected required_next to record failed evidence, got %#v", packet.RequiredNext)
	}
	joinedValid := strings.Join(packet.ValidNext, "\n")
	for _, want := range []string{"write the non-source deliverable", "<<QUESTION>>"} {
		if !strings.Contains(joinedValid, want) {
			t.Fatalf("expected valid_next to contain %q, got %#v", want, packet.ValidNext)
		}
	}
	joinedEvidence := strings.Join(packet.EvidenceStatus, "\n")
	for _, want := range []string{"closed_negative_evidence", "read_file", ".idiom/refine/example/iter-2/draft.md"} {
		if !strings.Contains(joinedEvidence, want) {
			t.Fatalf("expected evidence_status to contain %q, got %#v", want, packet.EvidenceStatus)
		}
	}
}

func TestScratchpadDeliverableFailedEvidenceKeepsOriginalFailure(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{".idiom/refine/example/iter-2/draft.md"},
	}
	result, ok := scratchpadDeliverableFailedEvidenceResult(brief, []workspace.ToolResult{
		{Tool: "code_navigate", Summary: "Code navigation routed references", Error: "no C++ symbols matched"},
		{Tool: "read_file", Summary: "Scratchpad evidence follow-up rejected", Error: "A previous optional evidence lookup failed"},
	})
	if !ok {
		t.Fatalf("expected original failed evidence result")
	}
	if result.Tool != "code_navigate" || strings.Contains(result.Error, "previous optional evidence") {
		t.Fatalf("expected original failure, got %#v", result)
	}
}

func TestScratchpadDeliverableFailedEvidenceIgnoresSourceImplementation(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"native/src/example.cpp"},
		},
	}
	if _, ok := runner.scratchpadDeliverableFailedEvidenceNudge(
		workspace.ToolRequest{Tool: "code_navigate", Capability: "references", Path: "native/src/example.cpp", Pattern: "MISSING_SYMBOL"},
		workspace.ToolResult{Tool: "code_navigate", Summary: "Code navigation routed references", Error: "no C++ symbols matched"},
		nil,
	); ok {
		t.Fatalf("source implementation failures should keep the normal recovery path")
	}
}

func TestPlanExecuteNonToolPlannerTextTriggersRequiredSourceRead(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go"},
		},
	}
	req, _, ok := runner.deterministicPlanExecuteRejectedRequiredSourceRead("I can make this change.", nil)
	if !ok {
		t.Fatalf("expected deterministic source read for non-tool planner text before required source evidence")
	}
	if req.Tool != "read_file" || req.Path != "cmd/idiom/product_workflow.go" {
		t.Fatalf("unexpected deterministic read request: %#v", req)
	}
}

func TestPlanExecuteNonToolPlannerQuestionDoesNotAutoRead(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"cmd/idiom/product_workflow.go"},
		},
	}
	if _, _, ok := runner.deterministicPlanExecuteRejectedRequiredSourceRead("<<QUESTION>>: missing permission", nil); ok {
		t.Fatalf("typed blocker questions should not be replaced by deterministic reads")
	}
}
