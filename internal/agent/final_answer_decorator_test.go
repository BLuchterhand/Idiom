package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/frontier/workerruntime"
	"idiom/internal/workspace"
)

func TestDecorateFinalAnswerStripsCompletedMutationSelfBlocker(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:           "implementation",
		Contract:             "implementation_result",
		PermissionProfile:    "guarded_workspace_write",
		Constraints:          []string{"planner_owned_verification"},
		RequiredEditPaths:    []string{"native/src/system.cpp", "native/tests/test_system.cpp"},
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited native/src/system.cpp"},
		{Tool: "edit_file", Summary: "Edited native/tests/test_system.cpp"},
		{
			Tool:    "apply_patch",
			Summary: "Patch hunk did not apply",
			Error:   "Update File native/src/system.cpp: hunk 1 context did not match the file",
		},
	}
	answer := "Changed paths:\n- native/src/system.cpp\n- native/tests/test_system.cpp\n\n" +
		"Verification: pending lane sanity; build/test verification is planner-owned.\n\n" +
		"Blocker:\n- Patch hunk did not apply: Update File native/src/system.cpp: hunk 1 context did not match the file"

	runner := &Runner{}
	got := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     answer,
		StopReason: "tool_step_budget_exhausted",
		Brief:      brief,
		Results:    results,
		UserFacing: true,
	})

	if strings.Contains(strings.ToLower(got), "blocker") {
		t.Fatalf("decorator should strip self-inflicted Blocker section, got:\n%s", got)
	}
	if !strings.Contains(got, "Changed paths") {
		t.Fatalf("decorator should preserve Changed paths summary, got:\n%s", got)
	}
	if !strings.Contains(got, "planner-owned") {
		t.Fatalf("decorator should preserve Verification line, got:\n%s", got)
	}
}

func TestDecorateFinalAnswerKeepsBlockerWhenRequiredPathsRemaining(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		Constraints:       []string{"planner_owned_verification"},
		RequiredEditPaths: []string{"a.go", "b.go"},
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited a.go"},
		{
			Tool:    "apply_patch",
			Summary: "Patch hunk did not apply",
			Error:   "Update File a.go: hunk 1 context did not match the file",
		},
	}
	answer := "Changed paths:\n- a.go\n\nBlocker:\n- Patch hunk did not apply on a.go"

	runner := &Runner{}
	got := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     answer,
		StopReason: "tool_step_budget_exhausted",
		Brief:      brief,
		Results:    results,
		UserFacing: true,
	})
	// Required path b.go remains unchanged; the strip helper must NOT
	// fire (otherwise we'd hide a real blocker). Caller should still
	// see the model's blocker prose.
	if !strings.Contains(strings.ToLower(got), "blocker") {
		t.Fatalf("decorator must preserve Blocker when required paths remain, got:\n%s", got)
	}
}

func TestDecorateFinalAnswerKeepsPlanExecuteSourceSatisfiedAnswer(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.go", "b.go"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.go lines 1-10"},
		{Tool: "read_file", Summary: "Read f2 b.go lines 1-10"},
	}
	answer := "Decision: source-satisfied\n- a.go: already implemented; no changes needed.\n- b.go: already implemented; no changes needed."

	runner := &Runner{}
	got := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     answer,
		StopReason: "model_stop",
		Brief:      brief,
		Results:    results,
		UserFacing: true,
	})
	if strings.Contains(got, "Required edit path") {
		t.Fatalf("decorator must not rewrite source-satisfied plan-execute answer into a blocker, got:\n%s", got)
	}
	if !strings.Contains(got, "source-satisfied") {
		t.Fatalf("decorator should preserve source-satisfied answer, got:\n%s", got)
	}
}

func TestDecorateFinalAnswerKeepsBlockerWhenVerificationNotPlannerOwned(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"a.go"},
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited a.go"},
		{
			Tool:    "apply_patch",
			Summary: "Patch hunk did not apply",
			Error:   "Update File a.go: hunk 1 context did not match the file",
		},
	}
	answer := "Changed paths:\n- a.go\n\nBlocker:\n- Patch hunk did not apply"

	runner := &Runner{}
	got := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     answer,
		StopReason: "tool_step_budget_exhausted",
		Brief:      brief,
		Results:    results,
		UserFacing: true,
	})
	if !strings.Contains(strings.ToLower(got), "blocker") {
		t.Fatalf("decorator should preserve Blocker when verification isn't planner-owned, got:\n%s", got)
	}
}

func TestDecorateFinalAnswerKeepsBlockerWhenLastFailureIsNonWriteTool(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		Constraints:       []string{"planner_owned_verification"},
		RequiredEditPaths: []string{"a.go"},
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited a.go"},
		{
			Tool:    "shell",
			Summary: "shell command failed",
			Error:   "exit status 1",
		},
	}
	answer := "Changed paths:\n- a.go\n\nBlocker:\n- shell command failed"

	runner := &Runner{}
	got := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     answer,
		StopReason: "tool_step_budget_exhausted",
		Brief:      brief,
		Results:    results,
		UserFacing: true,
	})
	if !strings.Contains(strings.ToLower(got), "blocker") {
		t.Fatalf("decorator should preserve Blocker when last failure was non-write tool, got:\n%s", got)
	}
}

func TestDecorateFinalAnswerSynthesizesReadOnlySourceReturnAnswer(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		Objective:         "What does internal/labels/labels.go ShortLabel return?",
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/labels/labels.go lines 1-5 (total lines: 5)",
		Output:  "1:package labels\n2:\n3:func ShortLabel() string {\n4:\treturn \"Save changes\"\n5:}\n",
	}}
	answer := "The retained source context identifies the relevant implementation surface as `internal/labels/labels.go`."

	runner := &Runner{}
	got := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     answer,
		StopReason: "rejection_retry_blocked",
		Brief:      brief,
		Results:    results,
		UserFacing: true,
	})
	for _, want := range []string{"internal/labels/labels.go", "ShortLabel", "\"Save changes\""} {
		if !strings.Contains(got, want) {
			t.Fatalf("decorated answer missing %q:\n%s", want, got)
		}
	}
	for _, forbidden := range []string{"Changing that returned string", "would be the source edit", "No files were changed", "contains `func ShortLabel"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("decorated answer should not infer edit advice %q:\n%s", forbidden, got)
		}
	}
}

func TestFinalAnswerDecoratorComposesFromTypedDelegatedPacketWithoutProseHeadings(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "architecture",
		Contract:          "architecture_assessment",
		PermissionProfile: "read_only_workspace",
		Objective:         "Can you tell me how this harness uses subagents?",
	}
	results := []workspace.ToolResult{typedReadOnlyTaskResult(ReadOnlyEvidencePacket{
		Objective:       brief.Objective,
		SourceBodyPaths: []string{"internal/agent/runner.go", "internal/agent/runner_child_tasks.go"},
		Findings: []ReadOnlyEvidenceFinding{
			{Path: "internal/agent/runner.go", Text: "The parent runner invokes the `task` tool to delegate read-only work."},
			{Path: "internal/agent/runner_child_tasks.go", Text: "`runChildTask` builds the bounded child runner and returns its result."},
		},
		Confidence: "high",
	})}
	runner := &Runner{}
	got := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     "Partial findings from this turn (the budget was reached before a full synthesis): internal packet available.",
		StopReason: "rejection_retry_blocked",
		Brief:      brief,
		Results:    results,
		UserFacing: true,
	})
	for _, want := range []string{"task", "runChildTask", "`internal/agent/runner.go`", "`internal/agent/runner_child_tasks.go`"} {
		if !strings.Contains(strings.ToLower(got), strings.ToLower(want)) {
			t.Fatalf("decorated typed packet answer missing %q:\n%s", want, got)
		}
	}
	for _, forbidden := range []string{"Partial findings", "Discovery packet", "Evidence used", "Inspected paths:", "Source-body evidence paths:", "Key findings:"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("decorated typed packet answer leaked %q:\n%s", forbidden, got)
		}
	}
}

func TestDecorateFinalAnswerBudgetStopDoesNotPreserveRetainedContextScaffold(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		Objective:         "What does internal/labels/labels.go ShortLabel return?",
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/labels/labels.go lines 1-5 (total lines: 5)",
		Output:  "1:package labels\n2:\n3:func ShortLabel() string {\n4:\treturn \"Save changes\"\n5:}\n",
	}}
	answer := "The retained source context identifies the relevant implementation surface as `internal/labels/labels.go`."

	runner := &Runner{}
	got := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     answer,
		StopReason: "tool_step_budget_exhausted",
		Brief:      brief,
		Results:    results,
		UserFacing: true,
	})
	for _, want := range []string{"internal/labels/labels.go", "ShortLabel", "\"Save changes\""} {
		if !strings.Contains(got, want) {
			t.Fatalf("decorated budget-stop answer missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "retained source context identifies") {
		t.Fatalf("budget-stop scaffold should not bypass decoration:\n%s", got)
	}
}

func TestPresentableReadFileSourceAnswerSkipsGeneratedArtifacts(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		Objective:         "Explain internal/app/history.go rendering.",
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 .idiom/comprehension/views/context.json lines 1-5 (total lines: 5)",
		Output:  "1:{\"candidate_files\":[{\"path\":\"internal/agent/runner.go\"}]}\n",
	}}
	if answer, ok := presentableReadFileSourceAnswer(brief, results, ""); ok {
		t.Fatalf("generated artifacts must not be converted into source-body answers, got %q", answer)
	}
}

func TestDecorateFinalAnswerDoesNotPromoteGeneratedArtifactGarbage(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		Objective:         "Explain how discovery tools help debug this codebase.",
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 .idiom/comprehension/views/context.json lines 1-5 (total lines: 5)",
		Output:  "1:{\"candidate_files\":[{\"path\":\"internal/agent/runner.go\"}]}\n",
	}}
	answer := "The inspected source in `.idiom/comprehension/views/context.json` contains `1:{\"candidate_files\":[{\"path\":\"internal/agent/runner.go\"}]}`. No files were changed.\n\nWhat this establishes:\n- 1:{\"candidate_files\":[{\"path\":\"internal/agent/runner.go\"}]}"

	runner := &Runner{}
	got := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     answer,
		StopReason: "tool_step_budget_exhausted",
		Brief:      brief,
		Results:    results,
		UserFacing: true,
	})
	if strings.Contains(got, ".idiom/comprehension/views/context.json") || strings.Contains(got, "candidate_files") {
		t.Fatalf("generated context artifact must not become a user-facing grounded answer:\n%s", got)
	}
}

func TestDecorateFinalAnswerAnswersReadOnlyTextChangeQuestion(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		Objective:         "Can we shorten the save label in internal/labels/labels.go?",
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/labels/labels.go lines 1-5 (total lines: 5)",
		Output:  "1:package labels\n2:\n3:func ShortLabel() string {\n4:\treturn \"Save changes\"\n5:}\n",
	}}
	answer := "tool: read_file\n{\"tool\":\"read_file\",\"path\":\"internal/labels/labels.go\"}"

	runner := &Runner{}
	got := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     answer,
		StopReason: "conversation_evidence_sufficient",
		Brief:      brief,
		Results:    results,
		UserFacing: true,
	})
	for _, want := range []string{"At the source level, yes", "internal/labels/labels.go", "\"Save changes\""} {
		if !strings.Contains(got, want) {
			t.Fatalf("decorated answer missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "No files were changed") || strings.Contains(got, "contains `func ShortLabel") {
		t.Fatalf("decorated answer should not echo source-body transcript:\n%s", got)
	}
	if strings.Contains(got, "tool: read_file") || strings.Contains(got, `{"tool"`) {
		t.Fatalf("decorated answer leaked internal tool request:\n%s", got)
	}
}

func TestDecorateFinalAnswerTreatsLineOrientedJSONToolAsInternal(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/labels/labels.go lines 1-5 (total lines: 5)",
		Output:  "1:package labels\n2:\n3:func ShortLabel() string {\n4:\treturn \"Save changes\"\n5:}\n",
	}}
	answer := "tool: read_file\n{\"tool\":\"read_file\",\"path\":\"internal/labels/labels.go\",\"start_line\":1,\"end_line\":200}"

	runner := &Runner{}
	got := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     answer,
		StopReason: "conversation_evidence_sufficient",
		Brief:      brief,
		Results:    results,
		UserFacing: true,
	})
	for _, want := range []string{"internal/labels/labels.go", "ShortLabel", "\"Save changes\""} {
		if !strings.Contains(got, want) {
			t.Fatalf("decorated answer missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "No files were changed") || strings.Contains(got, "contains `func ShortLabel") {
		t.Fatalf("decorated answer should not echo source-body transcript:\n%s", got)
	}
	if strings.Contains(got, "tool: read_file") || strings.Contains(got, `{"tool"`) {
		t.Fatalf("decorated answer leaked internal tool request:\n%s", got)
	}
}

func TestDecorateFinalAnswerSynthesizesRoutedCodeNavigateSourceBodyAnswer(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		Objective:         "Explain how task delegation isolates child runs.",
	}
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output: strings.Join([]string{
			"capability: read_symbol",
			"language: go",
			"routed_tool: go_read_symbol",
			"route_summary: Read Runner.launchReadOnlyChild in internal/agent/runner.go lines 100-150",
			"100:func (r *Runner) launchReadOnlyChild() {",
			"101:\tpolicy := RunPolicy{MaxToolSteps: 4, MaxModelCalls: 2}",
			"102:\tbrief := RuntimeBriefForRole(\"read_only_child\")",
			"103:\tchild := Runner{PermissionPolicy: readOnlyChildTaskPermissionPolicy(), RuntimeBrief: brief}",
			"104:\tchild.DisableTaskTool = true",
			"105:\tchild.Run(ctx, request)",
			"106:}",
		}, "\n"),
	}}
	answer := "Partial findings from this turn (the model stopped before a full synthesis):\n\nVerified context:\n- code_navigate internal/agent/runner.go"

	runner := &Runner{}
	got := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     answer,
		StopReason: "rejection_retry_blocked",
		Brief:      brief,
		Results:    results,
		UserFacing: true,
	})
	for _, want := range []string{
		"Runner.launchReadOnlyChild",
		"internal/agent/runner.go",
		"assigns `policy` from `RunPolicy{MaxToolSteps: 4, MaxModelCalls: 2}`",
		"assigns `brief` from `RuntimeBriefForRole(\"read_only_child\")`",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("decorated answer missing %q:\n%s", want, got)
		}
	}
	for _, forbidden := range []string{
		"bounded execution policy",
		"child-scoped runtime context",
		"disables nested task delegation",
		"nested child runner",
	} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("decorated answer used scenario-specific phrase %q:\n%s", forbidden, got)
		}
	}
	if strings.HasPrefix(got, "<<QUESTION>>") || strings.Contains(got, "did not retain a concrete user-facing finding") {
		t.Fatalf("routed source-body evidence should synthesize a concrete answer, got:\n%s", got)
	}
}

func TestPresentableExactProbeAnswerAcceptsReadOnlyCapabilitySourceBody(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		Objective:         "Explain the child runner policy.",
	}
	results := []workspace.ToolResult{{
		Tool:    "read_only_capability",
		Summary: "Read-only capability routed read_symbol via go_read_symbol",
		Output: strings.Join([]string{
			"capability: read_symbol",
			"route_summary: Read Runner.childPolicy in internal/agent/runner.go lines 20-40",
			"20:func (r *Runner) childPolicy() RunPolicy {",
			"21:\treturn RunPolicy{MaxToolSteps: 4, MaxModelCalls: 2}",
			"22:}",
		}, "\n"),
	}}

	got, ok := presentableExactProbeAnswer(brief, results)
	if !ok {
		t.Fatal("expected routed read_only_capability source body to be presentable")
	}
	for _, want := range []string{"Runner.childPolicy", "internal/agent/runner.go", "returns `RunPolicy{MaxToolSteps: 4, MaxModelCalls: 2}`"} {
		if !strings.Contains(got, want) {
			t.Fatalf("presentable answer missing %q:\n%s", want, got)
		}
	}
}

func TestExactProbeObservationsUseGenericSourceFacts(t *testing.T) {
	output := strings.Join([]string{
		"10:func run() string {",
		"11:\tpolicy := RunPolicy{MaxToolSteps: 4}",
		"12:\tchild.DisableTaskTool = true",
		"13:\tchild.Run(ctx, request)",
		"14:\treturn policy.Name",
		"15:}",
	}, "\n")

	got := strings.Join(exactProbeObservations("run", output), " ")
	for _, want := range []string{
		"assigns `policy` from `RunPolicy{MaxToolSteps: 4}`",
		"sets `child.DisableTaskTool` to `true`",
		"returns `policy.Name`",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generic exact-probe observations missing %q:\n%s", want, got)
		}
	}
	for _, forbidden := range []string{
		"delegated prompt",
		"bounded execution policy",
		"child-scoped runtime context",
		"permission policy",
		"nested task delegation",
		"nested child runner",
		"child answer",
		"task-result/evidence flow",
	} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("exact-probe observation used scenario-specific phrase %q:\n%s", forbidden, got)
		}
	}
}

func TestExactProbeObservationsDoNotInferChildRuntimeSemantics(t *testing.T) {
	output := strings.Join([]string{
		"10:func inspect() {",
		"11:\tnormalizeReadOnlyChildPrompt(prompt)",
		"12:\tRuntimeBriefForRole(\"read_only_child\")",
		"13:\treadOnlyChildTaskPermissionPolicy()",
		"14:\tchild.Run(ctx, request)",
		"15:\taugmentDelegatedReadOnlyPacket(packet)",
		"16:}",
	}, "\n")

	got := strings.Join(exactProbeObservations("inspect", output), " ")
	if !strings.Contains(got, "calls ") {
		t.Fatalf("expected generic call observation, got:\n%s", got)
	}
	for _, want := range []string{
		"normalizeReadOnlyChildPrompt",
		"RuntimeBriefForRole",
		"readOnlyChildTaskPermissionPolicy",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generic call observation missing %q:\n%s", want, got)
		}
	}
	for _, forbidden := range []string{
		"rewrites the delegated prompt",
		"bounded execution policy",
		"child-scoped runtime context",
		"permission policy instead of inheriting unrestricted access",
		"nested task delegation",
		"nested child runner",
		"folds the child answer",
		"task-result/evidence flow",
	} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("exact-probe observation inferred scenario semantics %q:\n%s", forbidden, got)
		}
	}
}

func TestPresentableExactProbeAnswerRejectsRoutedNonBodyEvidence(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		Objective:         "Explain the runner.",
	}
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed file_outline via go_file_outline",
			Output:  "capability: file_outline\nroute_summary: Outlined 2 top-level declaration(s) in internal/agent/runner.go.",
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output: strings.Join([]string{
				"capability: read_symbol",
				"route_summary: Read Candidate in .idiom/comprehension/views/context.json lines 1-2",
				"1:{\"candidate_files\":[{\"path\":\"internal/agent/runner.go\"}]}",
			}, "\n"),
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  `route_summary: Go symbol read path-filter miss; found 1 candidate anchor(s) for "Runner.Run"`,
		},
	}

	if got, ok := presentableExactProbeAnswer(brief, results); ok {
		t.Fatalf("non-body routed evidence must not be presentable, got:\n%s", got)
	}
}

func TestExactEvidenceSymbolSetAcceptsRoutedCodeNavigateSourceBody(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output: strings.Join([]string{
			"capability: read_symbol",
			"route_summary: Read Model.renderMessages in internal/app/rendering.go lines 459-520",
			"459:func (m *Model) renderMessages() {",
			"460:\tm.messagesViewport.SetContent(rendered)",
			"461:}",
		}, "\n"),
	}}

	symbols := exactEvidenceSymbolSet(results)
	for _, want := range []string{"Model.renderMessages", "SetContent"} {
		key := normalizedAnswerSymbol(want)
		if _, ok := symbols[key]; !ok {
			t.Fatalf("exact evidence symbol set missing %q (%q): %#v", want, key, symbols)
		}
	}
}

func TestPresentableNoToolsFallbackDoesNotReadWorkspaceFromPathOnlyEvidence(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "labels"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "labels", "labels.go"), []byte("package labels\n\nfunc ShortLabel() string {\n\treturn \"Save changes\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/labels/labels.go lines 1-5 (total lines: 5)",
	}}
	answer := "The retained source context identifies the relevant implementation surface as `internal/labels/labels.go`."

	got, ok := presentableNoToolsFallbackAnswer(brief, results, answer, root)
	if ok && strings.Contains(got, "\"Save changes\"") {
		t.Fatalf("fallback must not promote path-only evidence into source-body evidence by reading the workspace:\n%s", got)
	}
}

func TestPresentableNoToolsFallbackDoesNotUseSearchHitAsSourceBody(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
	}
	results := []workspace.ToolResult{{
		Tool:    "search_content",
		Summary: "Found 1 matching line(s).",
		Output:  "internal/labels/labels.go:4:\treturn \"Save changes\"",
	}}
	answer := "The retained source context identifies the relevant implementation surface as `internal/labels/labels.go`."

	got, ok := presentableNoToolsFallbackAnswer(brief, results, answer, "")
	if !ok {
		t.Fatal("expected fallback to convert unsupported scaffold into a blocker")
	}
	if !strings.HasPrefix(got, "<<QUESTION>>:") {
		t.Fatalf("expected blocker for search-only source evidence, got:\n%s", got)
	}
	if strings.Contains(got, "\"Save changes\"") {
		t.Fatalf("search hit text must not be promoted to source-body evidence:\n%s", got)
	}
}

func TestPresentableEvidenceBodySourceAnswerRejectsSearchHits(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		Objective:         "What does ShortLabel return?",
	}
	results := []workspace.ToolResult{{
		Tool:    "search_content",
		Summary: "Found 1 matching line(s).",
		Output:  "internal/labels/labels.go:4:\treturn \"Save changes\"",
	}}
	if got, ok := presentableEvidenceBodySourceAnswer(brief, results); ok {
		t.Fatalf("literal search hit must not be rendered as source-body answer:\n%s", got)
	}
}

func TestPresentableNoToolsFallbackRejectsEchoedQuestion(t *testing.T) {
	// Regression: the non-mutation guard must not return echoed user questions
	// as substantive answers. An answer that starts with "Question:" followed by
	// user text is not genuinely substantive and should fall through to
	// source-body composition.
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
	}
	results := []workspace.ToolResult{}
	answer := "Question: Can you investigate the strange spacing issues that seem to show? " +
		"It seems like we see hardcoded newlines that may be based on the initial size of the session, " +
		"but as the side panel opens, this causes output to have strange line breaks. " +
		"The history pane also has some weird forced newlines. Can we ensure that text wrapping is properly dynamic in all containers?\n\n" +
		"Source: delegated read-only evidence packet."

	got, ok := presentableNoToolsFallbackAnswer(brief, results, answer, "")
	if ok {
		t.Fatalf("expected fallback to reject echoed user question; got ok=true with:\n%s", got)
	}
}

func TestPresentableNoToolsFallbackPreservesSubstantiveAnswerAfterStrippingAppendix(t *testing.T) {
	// Regression: the non-mutation guard must still return genuinely substantive
	// answers after stripping internal appendix markers. The guard exists to
	// prefer the model's own prose over source-body extraction for conversation
	// questions. An answer with internal markers but genuine substantive content
	// should be cleaned and returned.
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/app/history.go lines 1-80",
		Output:  "package app\n\nfunc RenderHistory() {}\n",
	}}
	answer := "The implementation lives in internal/app/history.go. " +
		"The key function is RenderHistory which handles display layout " +
		"and wraps content based on available terminal width. " +
		"There are also helper functions in internal/app/layout.go that " +
		"control resizing behavior. The history pane uses a flex layout " +
		"where newlines are inserted at hard boundaries.\n\n" +
		"Key findings:\n- history.go is the primary rendering path\n- layout.go handles resizing\n\n" +
		"Confidence: high"

	got, ok := presentableNoToolsFallbackAnswer(brief, results, answer, "")
	if !ok {
		t.Fatal("expected fallback to preserve substantive answer")
	}
	if !strings.Contains(got, "internal/app/history.go") {
		t.Fatalf("fallback dropped content from substantive answer:\n%s", got)
	}
}

func TestStripInternalEvidenceAppendixStripsSourceLine(t *testing.T) {
	answer := "The implementation lives in internal/app/history.go.\n\nThe key function is RenderHistory.\n\n" +
		"Source: delegated read-only evidence packet."

	stripped := stripInternalEvidenceAppendix(answer)
	if strings.Contains(stripped, "Source:") {
		t.Fatalf("stripInternalEvidenceAppendix should strip lines starting with Source:\ngot: %s", stripped)
	}
	if !strings.Contains(stripped, "internal/app/history.go") {
		t.Fatalf("stripInternalEvidenceAppendix dropped substantive content:\ngot: %s", stripped)
	}
}

func TestStripBlockerSectionPreservesEverythingBeforeBlocker(t *testing.T) {
	answer := "Header line.\n\n" +
		"Changed paths:\n- foo.go\n\n" +
		"Verification: planner-owned.\n\n" +
		"Blocker:\n- failure detail one\n- failure detail two\n"
	cleaned, stripped := stripBlockerSection(answer)
	if !stripped {
		t.Fatal("expected stripBlockerSection to report a strip")
	}
	if strings.Contains(strings.ToLower(cleaned), "blocker") {
		t.Fatalf("cleaned answer should not contain Blocker section, got:\n%s", cleaned)
	}
	if !strings.Contains(cleaned, "foo.go") || !strings.Contains(cleaned, "Verification") {
		t.Fatalf("cleaned answer dropped earlier prose:\n%s", cleaned)
	}
	if strings.HasSuffix(cleaned, "\n") {
		t.Fatalf("cleaned answer should not have trailing newline, got %q", cleaned)
	}
}

func TestStripBlockerSectionNoOpWhenAbsent(t *testing.T) {
	answer := "Changed paths:\n- foo.go\n\nVerification: planner-owned."
	if _, stripped := stripBlockerSection(answer); stripped {
		t.Fatalf("stripBlockerSection should not mark a strip when no Blocker line exists")
	}
}

func TestWriteFailureTargetPathExtractsApplyPatchTarget(t *testing.T) {
	got := writeFailureTargetPath(workspace.ToolResult{
		Tool:    "apply_patch",
		Summary: "Patch hunk did not apply",
		Error:   "Update File native/src/foo.cpp: hunk 1 context did not match the file",
	})
	if got != "native/src/foo.cpp" {
		t.Fatalf("apply_patch target: want native/src/foo.cpp, got %q", got)
	}
}

func TestIsDeterministicFallbackAnswer(t *testing.T) {
	tests := []struct {
		name   string
		answer string
		want   bool
	}{
		{"exact fallback prefix", "Partial findings from this turn (the budget was reached before a full synthesis):\n\nVerified context:\n- read_file test.txt", true},
		{"lowercase", "partial findings from this turn (explore lane reached its budget before synthesizing):", true},
		{"substantive answer", "The evidence struct in internal/architect/packet.go tracks tool usage and context.", false},
		{"empty", "", false},
		{"unrelated prefix", "Based on my analysis, the evidence system supports...", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isDeterministicFallbackAnswer(tt.answer)
			if got != tt.want {
				t.Errorf("isDeterministicFallbackAnswer(%q) = %v, want %v", tt.answer, got, tt.want)
			}
		})
	}
}

func TestSynthesizeBriefConversationFallback(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: "conversation",
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read file: internal/architect/packet.go", Output: "type Evidence struct {..."},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "capability: read_symbol\nroute_summary: Read Model.resize in internal/app/model_state.go lines 774-820\n774:func (m *Model) resize() {"},
		{Tool: "context_query", Summary: "Context query returned 12 nodes"},
	}
	got := synthesizeBriefConversationFallback(brief, results)
	if !strings.HasPrefix(got, "<<QUESTION>>:") {
		t.Errorf("fallback should be a typed blocker, got: %q", got)
	}
	if !strings.Contains(got, "Evidence paths") {
		t.Errorf("fallback should mention evidence paths: %q", got)
	}
	if !strings.Contains(got, "Read Model.resize in internal/app/model_state.go lines 774-820") {
		t.Errorf("fallback should retain source anchors without dumping source body: %q", got)
	}
	if strings.Contains(got, "func (m *Model) resize") {
		t.Errorf("fallback should not dump source body: %q", got)
	}
	if strings.Contains(got, "No files were changed") {
		t.Errorf("fallback should not contain source-body extraction garbage: %q", got)
	}
	if strings.Contains(strings.ToLower(got), "budget") {
		t.Errorf("fallback must not claim budget exhaustion without a budget-exhausted stop reason: %q", got)
	}
	if !strings.Contains(got, "did not retain a concrete user-facing finding") {
		t.Errorf("fallback should identify the actual failure class: %q", got)
	}
}

func TestSynthesizeBriefConversationFallbackAnswersFromMarkdownEvidence(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		Objective:         "What is the role of evidence in this harness? Where does it come from and what is it used for?",
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 docs/idiom-strategic-thesis.md lines 1-320",
		Output: strings.Join([]string{
			"1:# Idiom Strategic Thesis",
			"5:Idiom sells the missing substrate: repo memory, freshness, contracts, audit, and evals.",
			"20:Idiom is the local, evidence-first coding-agent harness for teams that need agents to do real work in real repositories.",
			"21:The model owns strategy; the harness owns contracts, repository memory, verification, auditability, and the learning loop.",
			"146:The harness decides whether that action is allowed, whether evidence is fresh enough, and whether readiness is satisfied.",
			"287:Task-scoped repository evidence feeds the runtime brief, including tools, editable paths, evidence paths, verification, and budget.",
		}, "\n"),
	}}

	got := synthesizeBriefConversationFallback(brief, results)
	if strings.HasPrefix(got, "<<QUESTION>>") {
		t.Fatalf("answerable markdown evidence should synthesize an answer, got:\n%s", got)
	}
	for _, forbidden := range []string{"did not retain a concrete user-facing finding", "func ", "No files were changed"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("prose answer leaked forbidden fallback text %q:\n%s", forbidden, got)
		}
	}
	for _, want := range []string{"docs/idiom-strategic-thesis.md", "evidence-first", "contracts", "runtime brief"} {
		if !strings.Contains(got, want) {
			t.Fatalf("prose answer missing %q:\n%s", want, got)
		}
	}
}

func TestDecorateFinalAnswerConversationFallbackDoesNotInventBudgetExhaustion(t *testing.T) {
	runner := &Runner{}
	got := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     "Partial findings from this turn (the budget was reached before a full synthesis):\n\nVerified context:\n- read_file internal/architect/packet.go",
		StopReason: "model_final",
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			Objective:         "Explain internal/architect/packet.go.",
		},
		Results: []workspace.ToolResult{
			{Tool: "read_file", Summary: "Read file: internal/architect/packet.go", Output: "type Evidence struct {...}"},
			{Tool: "context_query", Summary: "Context query returned 12 nodes"},
		},
		UserFacing: true,
	})

	lower := strings.ToLower(got)
	for _, forbidden := range []string{"budget was exhausted", "turn budget", "budget was reached"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("model_final fallback must not invent budget exhaustion phrase %q:\n%s", forbidden, got)
		}
	}
	if !strings.Contains(got, "internal/architect/packet.go") {
		t.Fatalf("fallback should retain inspected path context, got:\n%s", got)
	}
}

func TestPresentableNoToolsFallbackSkipsSourceBodyForConversationFallback(t *testing.T) {
	// When the internal answer is a deterministic fallback and we're in a
	// read-only conversation lane, source-body extraction must not produce
	// garbage like "The inspected source contains `type Evidence struct {`..."
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		PermissionProfile: "read_only_workspace",
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read file: internal/architect/packet.go", Output: "type Evidence struct {\n\tKind  string `json:\"kind\"`\n\tLabel string `json:\"label,omitempty\"`\n}"},
	}
	internalAnswer := "Partial findings from this turn (the budget was reached before a full synthesis):\n\nVerified context:\n- read_file internal/architect/packet.go"

	got, ok := presentableNoToolsFallbackAnswer(brief, results, internalAnswer, "")
	if !ok {
		t.Fatalf("expected fallback to return ok=true")
	}
	if strings.Contains(got, "No files were changed") {
		t.Errorf("source-body extraction garbage leaked into conversation fallback: %q", got)
	}
	if !strings.Contains(got, "Evidence paths") {
		t.Errorf("fallback should provide useful evidence context: %q", got)
	}
}

func TestPresentableNoToolsFallbackSkipsSourceBodyAnswerForConversation(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		PermissionProfile: "read_only_workspace",
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read file: internal/labels/labels.go", Output: "func ShortLabel() string {\n\treturn \"Save changes\"\n}"},
	}
	internalAnswer := "The inspected source in `internal/labels/labels.go` contains `func ShortLabel() string {` and `return \"Save changes\"`. No files were changed."

	got, ok := presentableNoToolsFallbackAnswer(brief, results, internalAnswer, "")
	if !ok {
		t.Fatalf("expected fallback to return ok=true")
	}
	for _, forbidden := range []string{"No files were changed", "func ShortLabel"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("source-body fallback leaked %q into conversation answer: %q", forbidden, got)
		}
	}
	if !strings.Contains(got, "internal/labels/labels.go") {
		t.Errorf("fallback should retain inspected path context: %q", got)
	}
}

func TestDeterministicNoToolsAnswerSkipsSourceBodyForReadOnlyConversation(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		PermissionProfile: "read_only_workspace",
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read file: internal/labels/labels.go", Output: "func ShortLabel() string {\n\treturn \"Save changes\"\n}"},
	}
	got, ok := deterministicNoToolsAnswer("main", brief, results)
	if !ok {
		t.Fatal("expected deterministic answer")
	}
	for _, forbidden := range []string{"No files were changed", "func ShortLabel"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("deterministic answer leaked %q into conversation answer: %q", forbidden, got)
		}
	}
	if !strings.Contains(got, "internal/labels/labels.go") {
		t.Errorf("fallback should retain inspected path context: %q", got)
	}
}

func TestDeterministicNoToolsAnswerUsesSourceBodyWhenObjectiveIsConcrete(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		Objective:         "What is the debug log redacted mode alternative?",
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/transcript/debuglog.go lines 1-20",
		Output: strings.Join([]string{
			"1:package transcript",
			"2:",
			"3:const DebugLogModeRedacted = \"redacted\"",
			"4:const DebugLogModeFull = \"full\"",
			"5:// Debug log redacted mode omits prompts and tool payloads.",
		}, "\n"),
	}}

	got, ok := deterministicNoToolsAnswer("main", brief, results)
	if !ok {
		t.Fatal("expected deterministic source-body answer")
	}
	for _, want := range []string{"debug log redacted mode", "internal/transcript/debuglog.go", "DebugLogModeRedacted", "DebugLogModeFull"} {
		if !strings.Contains(got, want) {
			t.Fatalf("deterministic answer missing %q:\n%s", want, got)
		}
	}
	if strings.HasPrefix(got, "<<QUESTION>>") || strings.Contains(got, "did not retain a concrete user-facing finding") {
		t.Fatalf("concrete objective and source body should not collapse to blocker:\n%s", got)
	}
}
