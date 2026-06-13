package agent

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

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
		"put paths needing changes in `edits[]`",
		"top-level `source_satisfied_paths`",
		"never create an `edits[]` object whose only purpose is source-satisfied coverage",
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

func TestPlanExecuteNativeSubmitPlanCorrectionPromptNamesNoopPathRepair(t *testing.T) {
	prompt := planThenExecuteNativeSubmitPlanCorrectionPrompt(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.txt", "b.txt"},
	}, workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected",
		Error:   "edits[1]: edit would not change b.txt; for b.txt: remove no-op edit; use `source_satisfied_paths`: [\"b.txt\"] if final; use `Decision: source-satisfied` only if all paths are final; else submit real edit",
	})

	for _, forbidden := range []string{"Decision: source-satisfied", "<<QUESTION>>", "visible JSON object"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("native submit-correction prompt should not advertise %q:\n%s", forbidden, prompt)
		}
	}

	for _, want := range []string{
		"b.txt=use-source_satisfied_paths-if-current-state-is-final-or-submit-real-edit",
		"`source_satisfied_paths`: [\"b.txt\"]",
		"remove no-op edit; use `source_satisfied_paths`: [\"b.txt\"] if final",
		"The next tool call must be a corrected `submit_plan`",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("native no-op correction prompt missing %q:\n%s", want, prompt)
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

	if !strings.Contains(newFileBlock, `new_file: replace_all=true + old_text="" + new_text`) {
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

func TestPlanExecuteSourceSatisfiedTerminalSurfaceDoesNotAdvertiseSubmitPlanOnly(t *testing.T) {
	var refreshEvent string

	runner := &Runner{
		Client: llama.NewClient("http://example.invalid", time.Second).WithBackend(config.BackendMLX),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "plan_then_execute_textual_tool_surface_refreshed" {
				refreshEvent = event.Content
			}
		},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.go lines 1-10 (total lines: 10)", Output: "package a\n"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: source-satisfied terminal shape required",
			Error:   "submit_plan cannot carry a source-satisfied/no-op closeout. Answer `Decision: source-satisfied` with per-path current-state evidence, submit real edits, or answer <<QUESTION>> with the exact blocker.",
			Code:    workspace.SubmitPlanSourceSatisfiedTerminalRequiredCode,
		},
	}
	messages := []llama.ChatMessage{
		{Role: "system", Content: "# Terminal Plan-Execute Native Tool Surface\nNative `submit_plan` is the only exposed tool in this request."},
	}

	runner.refreshPlanThenExecuteTerminalToolSurface(&messages, results, "source_satisfied_terminal_required")

	joined := planExecuteJoinedMessages(messages)
	if !strings.Contains(joined, "# Terminal Plan-Execute Outcome Surface") ||
		!strings.Contains(joined, "Decision: source-satisfied") ||
		!strings.Contains(joined, "One visible textual `submit_plan` JSON object") {

		t.Fatalf("source-satisfied recovery should render terminal-outcome surface:\n%s", joined)
	}

	for _, forbidden := range []string{"Native `submit_plan` is the only exposed tool", "Required tools: submit_plan", "Allowed tools: submit_plan"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("source-satisfied terminal surface retained submit-plan-only guidance %q:\n%s", forbidden, joined)
		}
	}

	if !strings.Contains(refreshEvent, `"transport":"terminal_outcome"`) || !strings.Contains(refreshEvent, `"allowed_tools":[]`) {
		t.Fatalf("refresh event should report no forced submit_plan tools, got %q", refreshEvent)
	}
}

func TestPlanExecuteSourceSatisfiedRejectionAfterInvalidAnchorKeepsAnchorRefreshSurface(t *testing.T) {
	var refreshEvents []string

	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"read_file", "code_navigate", "submit_plan"},
		RequiredEditPaths: []string{"a.go", "b.go"},
	}
	runner := &Runner{
		RuntimeBrief: brief,
		OnEvent: func(event RunEvent) {
			if strings.Contains(event.Role, "plan_then_execute") {
				refreshEvents = append(refreshEvents, event.Role+":"+event.Content)
			}
		},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.go lines 1-10 (total lines: 10)", Output: "1:package a\n"},
		{Tool: "read_file", Summary: "Read f2 b.go lines 1-10 (total lines: 10)", Output: "1:package b\n"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "submit_plan must use edits[] for changed required paths and top-level source_satisfied_paths for already-final required paths; invalid edit anchor(s): `a.go: old_text not found in current source`.",
		},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: source-satisfied terminal shape required",
			Error:   "submit_plan cannot carry a source-satisfied/no-op closeout. Answer `Decision: source-satisfied` with per-path current-state evidence, submit real edits, or answer <<QUESTION>> with the exact blocker.",
			Code:    workspace.SubmitPlanSourceSatisfiedTerminalRequiredCode,
		},
	}

	surface := DerivePlanExecuteRecoverySurface(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if surface.Kind != PlanExecuteRecoverySurfaceAnchorRefresh {
		t.Fatalf("surface kind = %q, want anchor refresh; state=%#v", surface.Kind, surface.State)
	}

	if !slices.Equal(surface.State.RefreshPaths, []string{"a.go"}) {
		t.Fatalf("refresh paths = %#v, want a.go", surface.State.RefreshPaths)
	}

	opts := planThenExecuteReadyToSubmitChatOptionsForBackend(llama.ChatOptions{
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "code_navigate"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "auto",
	}, brief, results, true)
	if opts.ToolChoice != "required" || chatOptionsExposeTool(opts, "submit_plan") || !chatOptionsExposeTool(opts, "read_file") || !chatOptionsExposeTool(opts, "code_navigate") {
		t.Fatalf("source-satisfied rejection with open anchor refresh must expose only refresh tools, choice=%q tools=%#v", opts.ToolChoice, opts.Tools)
	}

	if opts.MaxTokens != planThenExecuteAnchorRefreshMaxTokens {
		t.Fatalf("anchor-refresh recovery should use refresh token cap, got %d", opts.MaxTokens)
	}

	if allowed := runnerPlanExecuteTextualAllowedTools(nil, brief, results); !slices.Equal(allowed, []string{"read_file", "code_navigate"}) {
		t.Fatalf("textual allowed tools = %#v, want anchor-refresh read tools", allowed)
	}
	if allowed := runnerPlanExecuteTextualAllowedTools([]string{"submit_plan"}, brief, results); !slices.Equal(allowed, []string{"read_file", "code_navigate"}) {
		t.Fatalf("submit-only prior surface must not veto anchor-refresh read tools, got %#v", allowed)
	}

	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief:             brief,
		Results:           results,
		BaseAllowedTools:  brief.AllowedTools,
		TextualToolParser: true,
		ToolTransportMode: "textual",
	})
	if contract.PhaseID != turnPhaseStaleAnchorRefresh ||
		!slices.Contains(contract.ActionPolicy.AllowedActionClasses, actionRead) ||
		slices.Contains(contract.ActionPolicy.AllowedActionClasses, actionSourceSatisfied) ||
		slices.Contains(contract.ActionPolicy.AllowedActionClasses, actionBlocker) {
		t.Fatalf("phase contract should stay read-only anchor-refresh, phase=%q actions=%#v", contract.PhaseID, contract.ActionPolicy.AllowedActionClasses)
	}

	messages := []llama.ChatMessage{
		{Role: "system", Content: "# Terminal Plan-Execute Outcome Surface\nNext visible action must be exactly one terminal outcome."},
		{Role: "system", Content: "# Terminal Plan-Execute Native Tool Surface\nNative `submit_plan` is the only exposed tool in this request."},
	}
	runner.refreshPlanThenExecuteTerminalToolSurface(&messages, results, "source_satisfied_terminal_required")

	joined := planExecuteJoinedMessages(messages)
	if !strings.Contains(joined, "# Plan-Execute Anchor Refresh Surface") ||
		!strings.Contains(joined, "Refresh only: a.go") ||
		!strings.Contains(joined, "Do not emit `submit_plan` until this bounded refresh returns") {

		t.Fatalf("refreshed surface should present only anchor-refresh guidance:\n%s", joined)
	}

	for _, forbidden := range []string{
		"# Terminal Plan-Execute Outcome Surface",
		"# Terminal Plan-Execute Native Tool Surface",
		"with per-path current-state evidence if every required path already contains the requested final state",
		"Next visible action must be exactly one terminal outcome",
		"Native `submit_plan` is the only exposed tool",
	} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("anchor-refresh surface retained contradictory guidance %q:\n%s", forbidden, joined)
		}
	}

	if len(refreshEvents) != 1 ||
		!strings.Contains(refreshEvents[0], "plan_then_execute_anchor_refresh_surface_refreshed") ||
		!strings.Contains(refreshEvents[0], `"allowed_tools":["read_file","code_navigate"]`) {

		t.Fatalf("expected one anchor-refresh surface event, got %#v", refreshEvents)
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

func TestPlanExecuteRejectedSubmitPlanDraftEvidenceSanitizesNoopGuidance(t *testing.T) {
	prompt := planThenExecuteRejectedSubmitPlanDraftEvidence(workspace.ToolRequest{
		Tool: "submit_plan",
		Edits: []workspace.PlanEdit{{
			Path:    "b.txt",
			OldText: "before\n",
			NewText: "before\n",
			Purpose: "no-op edit",
		}},
	}, workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected",
		Error:   "edits[0]: edit would not change b.txt; answer `Decision: source-satisfied`, use `source_satisfied_paths` for mixed coverage, or submit real edits",
	})

	if strings.Contains(prompt, "Decision: source-satisfied") {
		t.Fatalf("rejected draft evidence should not replay terminal source-satisfied in submit-correction stack:\n%s", prompt)
	}

	if !strings.Contains(prompt, "source_satisfied_paths") || !strings.Contains(prompt, "submit real edits") {
		t.Fatalf("rejected draft evidence should preserve corrected submit_plan alternatives:\n%s", prompt)
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

func TestPlanExecuteSubmitCorrectionRejectionBriefRemovesTerminalSourceSatisfied(t *testing.T) {
	brief := lastSubmitPlanCorrectionRejectionBrief(workspace.ToolResult{
		Tool:  "submit_plan",
		Error: "edits[1]: edit would not change b.txt; answer `Decision: source-satisfied`, use `source_satisfied_paths` for mixed coverage, or submit real edits",
	})

	if strings.Contains(brief, "Decision: source-satisfied") {
		t.Fatalf("submit-correction brief must not advertise terminal source-satisfied:\n%s", brief)
	}

	if !strings.Contains(brief, "source_satisfied_paths") || !strings.Contains(brief, "submit real edits") {
		t.Fatalf("submit-correction brief should preserve submit_plan alternatives:\n%s", brief)
	}
}

func TestPlanExecuteSubmitCorrectionInvalidShapeSeparatesSourceSatisfiedCoverage(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.txt", "b.txt"},
	}
	result := workspace.ToolResult{
		Tool:    "submit_plan",
		Summary: "submit_plan rejected",
		Error:   "plan_artifact.schema_errors edits[1]:invalid_edit_shape:requires one executable edit shape: old_text with new_text. If this path is already at the requested final state, remove it from edits[] and put the path in top-level source_satisfied_paths instead of using an edit object",
	}

	instruction := planThenExecuteSubmitPlanCorrectionInstructionForBrief(brief, result)
	for _, want := range []string{
		"put paths needing changes in `edits[]`",
		"top-level `source_satisfied_paths`",
		"never create an `edits[]` object whose only purpose is source-satisfied coverage",
		"must-have-valid-edit-or-source_satisfied_paths-entry",
	} {
		if !strings.Contains(instruction, want) {
			t.Fatalf("invalid-shape correction instruction missing %q:\n%s", want, instruction)
		}
	}

	if strings.Contains(instruction, "Decision: source-satisfied") {
		t.Fatalf("submit-correction invalid-shape instruction must not advertise terminal source-satisfied:\n%s", instruction)
	}
}

func TestPlanExecuteSubmitPlanSkeletonExplainsSourceSatisfiedPathsAreTopLevel(t *testing.T) {
	skeleton := planThenExecuteTextualSubmitPlanSkeleton([]planThenExecuteSubmitPlanSkeletonEntry{{
		Path:      "a.txt",
		StartLine: 1,
		EndLine:   3,
		Evidence:  []string{"Read f1 a.txt lines 1-3 (total lines: 3)"},
	}})
	for _, want := range []string{
		"top-level `source_satisfied_paths`",
		"never create an `edits[]` object whose only purpose is source-satisfied coverage",
		"top-level `tool:\"submit_plan\"`, `rationale`, and an `edits` array",
	} {
		if !strings.Contains(skeleton, want) {
			t.Fatalf("submit_plan skeleton missing %q:\n%s", want, skeleton)
		}
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
