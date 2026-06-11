package agent

import (
	"strings"
	"testing"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/workerruntime"
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
		SourceBodyPaths: []string{"internal/runtime/agent/runner.go", "internal/runtime/agent/runner_child_tasks.go"},
		Findings: []ReadOnlyEvidenceFinding{
			{Path: "internal/runtime/agent/runner.go", Text: "The harness uses the `task` tool to delegate read-only subagent work."},
			{Path: "internal/runtime/agent/runner_child_tasks.go", Text: "`runChildTask` builds the bounded subagent child runner and returns its result."},
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
	for _, want := range []string{"task", "runChildTask", "`internal/runtime/agent/runner.go`", "`internal/runtime/agent/runner_child_tasks.go`"} {
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

func TestFinalAnswerDecoratorReplacesLineOrientedSourceSnippetWithTypedPacket(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "architecture",
		Contract:          "architecture_assessment",
		PermissionProfile: "read_only_workspace",
		Objective:         "Can you tell me how this harness uses subagents?",
	}
	results := []workspace.ToolResult{typedReadOnlyTaskResult(ReadOnlyEvidencePacket{
		Objective:       brief.Objective,
		SourceBodyPaths: []string{"internal/runtime/agent/runner.go", "internal/runtime/agent/runner_child_tasks.go"},
		Findings: []ReadOnlyEvidenceFinding{
			{Path: "internal/runtime/agent/runner.go", Text: "The harness uses the `task` tool to delegate read-only subagent work."},
			{Path: "internal/runtime/agent/runner_child_tasks.go", Text: "`runChildTask` builds the bounded subagent child runner and returns its result."},
		},
		Confidence: "high",
	})}
	answer := "For `Can you tell me how this harness uses subagents?`, 1:package agent. 2:. 3:import (. This is grounded in `internal/runtime/agent/runner.go`."

	runner := &Runner{}
	got := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     answer,
		StopReason: "model_stop",
		Brief:      brief,
		Results:    results,
		UserFacing: true,
	})
	for _, want := range []string{"task", "runChildTask", "`internal/runtime/agent/runner.go`", "`internal/runtime/agent/runner_child_tasks.go`"} {
		if !strings.Contains(strings.ToLower(got), strings.ToLower(want)) {
			t.Fatalf("decorated typed packet answer missing %q:\n%s", want, got)
		}
	}
	for _, forbidden := range []string{"1:package", "2:.", "3:import"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("decorated answer leaked line-oriented source snippet %q:\n%s", forbidden, got)
		}
	}
}

func TestFinalAnswerDecoratorDoesNotPromoteOffObjectiveTaskOutput(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "architecture",
		Contract:          "architecture_assessment",
		PermissionProfile: "read_only_workspace",
		Objective:         "Can you tell me how this harness uses subagents?",
	}
	results := []workspace.ToolResult{{
		Tool:    "task",
		Summary: "Child task completed",
		Output: strings.Join([]string{
			"Source-body evidence paths:",
			"- `internal/runtime/agent/runner.go`",
			"",
			"Key findings:",
			"- The runner records model-call metadata in internal/runtime/agent/runner.go.",
			"Confidence: high.",
		}, "\n"),
	}}

	runner := &Runner{}
	if answer, ok := runner.composedFinalAnswerFromEvidence(FinalAnswerDecorateInput{
		Brief:   brief,
		Results: results,
	}); ok {
		t.Fatalf("off-objective task output must not be promoted by final-answer decoration, got:\n%s", answer)
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
		Objective:         "Explain internal/surfaces/tui/app/history.go rendering.",
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 .idiom/comprehension/views/context.json lines 1-5 (total lines: 5)",
		Output:  "1:{\"candidate_files\":[{\"path\":\"internal/runtime/agent/runner.go\"}]}\n",
	}}
	if answer, ok := presentableReadFileSourceAnswer(brief, results); ok {
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
		Output:  "1:{\"candidate_files\":[{\"path\":\"internal/runtime/agent/runner.go\"}]}\n",
	}}
	answer := "The inspected source in `.idiom/comprehension/views/context.json` contains `1:{\"candidate_files\":[{\"path\":\"internal/runtime/agent/runner.go\"}]}`. No files were changed.\n\nWhat this establishes:\n- 1:{\"candidate_files\":[{\"path\":\"internal/runtime/agent/runner.go\"}]}"

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
			"route_summary: Read Runner.launchReadOnlyChild in internal/runtime/agent/runner.go lines 100-150",
			"100:func (r *Runner) launchReadOnlyChild() {",
			"101:\tpolicy := RunPolicy{MaxToolSteps: 4, MaxModelCalls: 2}",
			"102:\tbrief := RuntimeBriefForRole(\"read_only_child\")",
			"103:\tchild := Runner{PermissionPolicy: readOnlyChildTaskPermissionPolicy(), RuntimeBrief: brief}",
			"104:\tchild.DisableTaskTool = true",
			"105:\tchild.Run(ctx, request)",
			"106:}",
		}, "\n"),
	}}
	answer := "Partial findings from this turn (the model stopped before a full synthesis):\n\nVerified context:\n- code_navigate internal/runtime/agent/runner.go"

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
		"internal/runtime/agent/runner.go",
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
			"route_summary: Read Runner.childPolicy in internal/runtime/agent/runner.go lines 20-40",
			"20:func (r *Runner) childPolicy() RunPolicy {",
			"21:\treturn RunPolicy{MaxToolSteps: 4, MaxModelCalls: 2}",
			"22:}",
		}, "\n"),
	}}

	got, ok := presentableExactProbeAnswer(brief, results)
	if !ok {
		t.Fatal("expected routed read_only_capability source body to be presentable")
	}
	for _, want := range []string{"Runner.childPolicy", "internal/runtime/agent/runner.go", "returns `RunPolicy{MaxToolSteps: 4, MaxModelCalls: 2}`"} {
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
			Output:  "capability: file_outline\nroute_summary: Outlined 2 top-level declaration(s) in internal/runtime/agent/runner.go.",
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output: strings.Join([]string{
				"capability: read_symbol",
				"route_summary: Read Candidate in .idiom/comprehension/views/context.json lines 1-2",
				"1:{\"candidate_files\":[{\"path\":\"internal/runtime/agent/runner.go\"}]}",
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
			"route_summary: Read Model.renderMessages in internal/surfaces/tui/app/rendering.go lines 459-520",
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

	got, ok := presentableNoToolsFallbackAnswer(brief, results, answer)
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

	got, ok := presentableNoToolsFallbackAnswer(brief, results, answer)
	if !ok {
		t.Fatal("expected fallback to convert unsupported scaffold into presentable uncertainty")
	}
	if strings.HasPrefix(got, "<<QUESTION>>:") {
		t.Fatalf("direct-answer search-only evidence must not expose blocker protocol, got:\n%s", got)
	}
	if !strings.Contains(got, "not have enough supported evidence") ||
		!strings.Contains(got, "internal/labels/labels.go") {
		t.Fatalf("expected presentable uncertainty with candidate context, got:\n%s", got)
	}
	if strings.Contains(got, "\"Save changes\"") {
		t.Fatalf("search hit text must not be promoted to source-body evidence:\n%s", got)
	}
}

func TestDecorateFinalAnswerSanitizesReadOnlyQuestionBlocker(t *testing.T) {
	runner := &Runner{}
	got := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     "<<QUESTION>>: Completion gate rejected the final answer repeatedly before the required completion evidence was produced.\n\nMissing completion evidence: the final answer uses inspected source evidence, but that evidence does not establish the user's objective. Answer with the exact objective-relevant source-body evidence, state that the current evidence is insufficient, or use <<QUESTION>>: with the exact missing evidence.",
		StopReason: "answer_objective_unmet",
		Brief: workerruntime.WorkerRuntimeBrief{
			Contract:          string(workerruntime.ContractDirectAnswer),
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			PermissionProfile: string(workerruntime.PermissionReadOnlyWorkspace),
			Objective:         "Find why the TUI chat input first wraps to a newline.",
		},
		Results: []workspace.ToolResult{{
			Tool:    "read_file",
			Summary: "Read internal/surfaces/tui/app/rendering.go lines 1-9",
			Output:  "1:package app\n2:\n3:func evidenceDisplayName(rawTool string) string { return strings.TrimSpace(rawTool) }\n",
		}},
		UserFacing: true,
	})

	if strings.Contains(got, "<<QUESTION>>") {
		t.Fatalf("user-facing decorator leaked internal blocker protocol:\n%s", got)
	}
	for _, want := range []string{"not have enough supported evidence", "internal/surfaces/tui/app/rendering.go"} {
		if !strings.Contains(strings.ToLower(got), strings.ToLower(want)) {
			t.Fatalf("decorated blocker missing %q:\n%s", want, got)
		}
	}
	for _, forbidden := range []string{"<<question>>", "completion gate", "missing completion evidence", "answer with the exact", "required next evidence"} {
		if strings.Contains(strings.ToLower(got), forbidden) {
			t.Fatalf("decorated blocker leaked internal phrase %q:\n%s", forbidden, got)
		}
	}
}

func TestDecorateFinalAnswerDoesNotRewriteNormalReadOnlyAnswer(t *testing.T) {
	runner := &Runner{}
	answer := "The inspected source shows that rendering delegates wrapping to the layout width calculation."
	got := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer: answer,
		Brief: workerruntime.WorkerRuntimeBrief{
			Contract:          string(workerruntime.ContractDirectAnswer),
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			PermissionProfile: string(workerruntime.PermissionReadOnlyWorkspace),
		},
		UserFacing: true,
	})
	if got != answer {
		t.Fatalf("normal read-only answer should pass through unchanged, got:\n%s", got)
	}
}

func TestDecorateFinalAnswerSanitizesConversationOnlyQuestionBlocker(t *testing.T) {
	runner := &Runner{}
	got := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer: "<<QUESTION>>: Cannot answer because the requested external fact is unavailable.",
		Brief: workerruntime.WorkerRuntimeBrief{
			Contract:          string(workerruntime.ContractDirectAnswer),
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			PermissionProfile: string(workerruntime.PermissionConversationOnly),
		},
		UserFacing: true,
	})
	if got != "Cannot answer because the requested external fact is unavailable." {
		t.Fatalf("conversation-only blocker should be presentable without protocol marker, got:\n%s", got)
	}
}

func TestDecorateFinalAnswerPreservesExternalQuestionBlockerEvidenceTerms(t *testing.T) {
	runner := &Runner{}
	brief := workerruntime.WorkerRuntimeBrief{
		Contract:          string(workerruntime.ContractDirectAnswer),
		TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
		PermissionProfile: string(workerruntime.PermissionReadOnlyWorkspace),
	}
	cases := []struct {
		name   string
		answer string
		want   string
	}{
		{
			name:   "source body evidence",
			answer: "<<QUESTION>>: Cannot answer because source-body evidence from the requested generated file is unavailable.",
			want:   "Cannot answer because source-body evidence from the requested generated file is unavailable.",
		},
		{
			name:   "diagnostic evidence",
			answer: "<<QUESTION>>: Cannot answer until diagnostic evidence from the failed job is attached.",
			want:   "Cannot answer until diagnostic evidence from the failed job is attached.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
				Answer:     tc.answer,
				Brief:      brief,
				UserFacing: true,
			})
			if got != tc.want {
				t.Fatalf("external blocker should preserve evidence terminology without protocol marker, got:\n%s", got)
			}
		})
	}
}

func TestCombinedReadFileSourceBodiesIncludesPrefetchEvidence(t *testing.T) {
	results := []workspace.ToolResult{
		{
			Tool:    workspace.ToolNamePrefetch,
			Summary: "Read f1 README.md lines 1-1 (total lines: 1; source: prefetch)",
			Output:  "1:Idiom is a local coding-agent harness.\n",
		},
	}

	bodies := combinedReadFileSourceBodies(results)
	if len(bodies) != 1 {
		t.Fatalf("expected one source body from prefetch, got %#v", bodies)
	}
	if bodies[0].path != "README.md" || !strings.Contains(bodies[0].output, "local coding-agent harness") {
		t.Fatalf("unexpected prefetch source body: %#v", bodies[0])
	}
}

func TestDecorateFinalAnswerSanitizesInternalReadOnlyRemediationQuestionBlocker(t *testing.T) {
	runner := &Runner{}
	got := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer: "<<QUESTION>>: The current read-only answer is not backed by relevant source-body or diagnostic evidence. Gather one source-body read for the claim, or name the exact missing claim.",
		Brief: workerruntime.WorkerRuntimeBrief{
			Contract:          string(workerruntime.ContractDirectAnswer),
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			PermissionProfile: string(workerruntime.PermissionReadOnlyWorkspace),
			Objective:         "Explain why the latest run failed.",
		},
		Results: []workspace.ToolResult{{
			Tool:    "read_file",
			Summary: "Read logs/conversations/session.jsonl lines 1-20",
			Output:  "1:{\"event\":\"model_stop\"}\n",
		}},
		UserFacing: true,
	})

	if strings.Contains(got, "<<QUESTION>>") {
		t.Fatalf("decorator leaked internal blocker protocol:\n%s", got)
	}
	for _, forbidden := range []string{"current read-only answer", "gather one source-body read", "exact missing claim"} {
		if strings.Contains(strings.ToLower(got), forbidden) {
			t.Fatalf("decorated blocker leaked internal remediation phrase %q:\n%s", forbidden, got)
		}
	}
	if !strings.Contains(got, "not have enough supported evidence") || !strings.Contains(got, "logs/conversations/session.jsonl") {
		t.Fatalf("decorated blocker should keep user-facing stop context and evidence path, got:\n%s", got)
	}
}

func TestDecorateFinalAnswerPreservesPlanExecuteReviewRepairQuestionBlocker(t *testing.T) {
	runner := &Runner{}
	answer := "<<QUESTION>>: Cannot repair because evidence is unavailable."
	got := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer: answer,
		Brief: workerruntime.WorkerRuntimeBrief{
			Contract:          "review_findings",
			TaskFamily:        string(workerruntime.TaskFamilyPlanThenExecuteReview),
			PermissionProfile: string(workerruntime.PermissionReadOnlyWorkspace),
		},
		UserFacing: true,
	})
	if got != answer {
		t.Fatalf("plan-execute review-repair blocker must remain machine-readable, got:\n%s", got)
	}
}

func TestDecorateFinalAnswerSanitizesSynthesizedReadOnlyQuestionBlocker(t *testing.T) {
	runner := &Runner{}
	got := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer: "The retained source context identifies the relevant implementation surface as `internal/labels/labels.go`.",
		Brief: workerruntime.WorkerRuntimeBrief{
			Contract:          string(workerruntime.ContractDirectAnswer),
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			PermissionProfile: string(workerruntime.PermissionReadOnlyWorkspace),
			Objective:         "What does ShortLabel return?",
		},
		Results: []workspace.ToolResult{{
			Tool:    "search_content",
			Summary: "Found 1 matching line(s).",
			Output:  "internal/labels/labels.go:4:\treturn \"Save changes\"",
		}},
		UserFacing: true,
	})

	if strings.Contains(got, "<<QUESTION>>") {
		t.Fatalf("decorator leaked synthesized internal blocker protocol:\n%s", got)
	}
	if !strings.Contains(got, "not have enough supported evidence") || !strings.Contains(got, "internal/labels/labels.go") {
		t.Fatalf("decorated synthesized blocker lost useful context:\n%s", got)
	}
	for _, forbidden := range []string{"required next evidence", "answer with the exact"} {
		if strings.Contains(strings.ToLower(got), forbidden) {
			t.Fatalf("decorated synthesized blocker leaked internal phrase %q:\n%s", forbidden, got)
		}
	}
}

func TestDecorateFinalAnswerLabelsCandidateOnlyReadOnlyBlocker(t *testing.T) {
	runner := &Runner{}
	got := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     "<<QUESTION>>: Completion gate rejected the final answer before source-body evidence was gathered.",
		StopReason: "user_visible_blocker",
		Brief: workerruntime.WorkerRuntimeBrief{
			Contract:          string(workerruntime.ContractDirectAnswer),
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			PermissionProfile: string(workerruntime.PermissionReadOnlyWorkspace),
			Objective:         "What does ShortLabel return?",
		},
		Results: []workspace.ToolResult{{
			Tool:    "search_content",
			Summary: "Found 1 matching line(s).",
			Output:  "internal/labels/labels.go:4:\treturn \"Save changes\"",
		}},
		UserFacing: true,
	})

	if strings.Contains(got, "<<QUESTION>>") {
		t.Fatalf("decorator leaked internal blocker protocol:\n%s", got)
	}
	if !strings.Contains(got, "Candidate paths surfaced") || !strings.Contains(got, "internal/labels/labels.go") {
		t.Fatalf("candidate-only blocker should retain candidate context with honest label:\n%s", got)
	}
	if strings.Contains(got, "Inspected evidence") {
		t.Fatalf("candidate-only blocker must not claim inspected evidence:\n%s", got)
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

	got, ok := presentableNoToolsFallbackAnswer(brief, results, answer)
	if ok {
		t.Fatalf("expected fallback to reject echoed user question; got ok=true with:\n%s", got)
	}
}

func TestPresentableUnsupportedFinalAnswerLeadMatchesTUIContract(t *testing.T) {
	got := presentableUnsupportedFinalAnswerLead()
	for _, want := range []string{
		"could not produce a supported final answer",
		"run stopped before",
		"not have enough supported evidence",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("unsupported final-answer lead missing %q:\n%s", want, got)
		}
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
		Summary: "Read f1 internal/surfaces/tui/app/history.go lines 1-80",
		Output:  "package app\n\nfunc RenderHistory() {}\n",
	}}
	answer := "The implementation lives in internal/surfaces/tui/app/history.go. " +
		"The key function is RenderHistory which handles display layout " +
		"and wraps content based on available terminal width. " +
		"There are also helper functions in internal/surfaces/tui/app/layout.go that " +
		"control resizing behavior. The history pane uses a flex layout " +
		"where newlines are inserted at hard boundaries.\n\n" +
		"Key findings:\n- history.go is the primary rendering path\n- layout.go handles resizing\n\n" +
		"Confidence: high"

	got, ok := presentableNoToolsFallbackAnswer(brief, results, answer)
	if !ok {
		t.Fatal("expected fallback to preserve substantive answer")
	}
	if !strings.Contains(got, "internal/surfaces/tui/app/history.go") {
		t.Fatalf("fallback dropped content from substantive answer:\n%s", got)
	}
}

func TestStripInternalEvidenceAppendixStripsSourceLine(t *testing.T) {
	answer := "The implementation lives in internal/surfaces/tui/app/history.go.\n\nThe key function is RenderHistory.\n\n" +
		"Source: delegated read-only evidence packet."

	stripped := stripInternalEvidenceAppendix(answer)
	if strings.Contains(stripped, "Source:") {
		t.Fatalf("stripInternalEvidenceAppendix should strip lines starting with Source:\ngot: %s", stripped)
	}
	if !strings.Contains(stripped, "internal/surfaces/tui/app/history.go") {
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
		{"substantive answer", "The evidence struct in internal/workflows/architect/packet.go tracks tool usage and context.", false},
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
		{Tool: "read_file", Summary: "Read file: internal/workflows/architect/packet.go", Output: "type Evidence struct {..."},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "capability: read_symbol\nroute_summary: Read Model.resize in internal/surfaces/tui/app/model_state.go lines 774-820\n774:func (m *Model) resize() {"},
		{Tool: "context_query", Summary: "Context query returned 12 nodes"},
	}
	got := synthesizeBriefConversationFallback(brief, results)
	if strings.HasPrefix(got, "<<QUESTION>>:") {
		t.Errorf("read-only conversation fallback must not expose typed blocker protocol, got: %q", got)
	}
	if !strings.Contains(got, "Evidence paths") {
		t.Errorf("fallback should mention evidence paths: %q", got)
	}
	if !strings.Contains(got, "Read Model.resize in internal/surfaces/tui/app/model_state.go lines 774-820") {
		t.Errorf("fallback should retain source anchors without dumping source body: %q", got)
	}
	if strings.Contains(got, "func (m *Model) resize") {
		t.Errorf("fallback should not dump source body: %q", got)
	}
	if strings.Contains(strings.ToLower(got), "files were changed") ||
		strings.Contains(strings.ToLower(got), "did not change any files") {
		t.Errorf("fallback should not contain source-body extraction garbage: %q", got)
	}
	if strings.Contains(strings.ToLower(got), "budget") {
		t.Errorf("fallback must not claim budget exhaustion without a budget-exhausted stop reason: %q", got)
	}
	if !strings.Contains(got, "did not retain a concrete user-facing finding") {
		t.Errorf("fallback should identify the actual failure class: %q", got)
	}
	if !strings.Contains(got, "not have enough supported evidence") {
		t.Errorf("fallback should present uncertainty as normal prose: %q", got)
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

func TestSynthesizeBriefConversationFallbackAnswersFromInspectedExplainerSymbol(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		Objective:         "Explain the harness control flow.",
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read internal/runtime/agent/runner.go lines 1-20",
		Output: strings.Join([]string{
			"1:package agent",
			"2:func (r *Runner) Run() {}",
		}, "\n"),
	}}

	got := synthesizeBriefConversationFallback(brief, results)
	if strings.HasPrefix(got, "<<QUESTION>>") {
		t.Fatalf("answerable inspected symbol should synthesize an answer, got:\n%s", got)
	}
	for _, want := range []string{"internal/runtime/agent/runner.go", "Runner.Run"} {
		if !strings.Contains(got, want) {
			t.Fatalf("symbol answer missing %q:\n%s", want, got)
		}
	}

	results = append(results,
		workspace.ToolResult{
			Tool:    "read_file",
			Summary: "Post-readiness named_gap is an explainer ask, not a falsifiable claim",
			Error:   "the named_gap reads as an explainer rather than naming a specific symbol body",
		},
		workspace.ToolResult{
			Tool:    "read_file",
			Summary: "Post-readiness named_gap is an explainer ask, not a falsifiable claim",
			Error:   "the named_gap reads as an explainer rather than naming a specific symbol body",
		},
	)
	got = synthesizeBriefConversationFallback(brief, results)
	if strings.HasPrefix(got, "<<QUESTION>>") {
		t.Fatalf("post-readiness rejections must not hide answerable source evidence, got:\n%s", got)
	}
	for _, want := range []string{"internal/runtime/agent/runner.go", "Runner.Run"} {
		if !strings.Contains(got, want) {
			t.Fatalf("post-rejection symbol answer missing %q:\n%s", want, got)
		}
	}
}

func TestSynthesizeBriefConversationFallbackAnswersWhatWouldItTakeFromKeyHandlerBody(t *testing.T) {
	objective := "What would it take to enable the up and down arrow keys to iterate through old user messages as a shortcut to retry previous messages?"
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		Objective:         objective,
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read internal/surfaces/tui/app/update_handlers.go lines 22-182",
			Output: strings.Join([]string{
				"22:func (m *Model) handleKeyPress(msg tea.KeyMsg) (tea.Cmd, bool) {",
				"43:\t\tcase tea.KeyUp:",
				"44:\t\t\tif m.slashSuggestions.selected > 0 {",
				"49:\t\tcase tea.KeyDown:",
				"50:\t\t\tif m.slashSuggestions.selected < len(m.slashSuggestions.matches)-1 {",
				"69:\t\tcase tea.KeyUp:",
				"70:\t\t\tif m.history.selected > 0 {",
				"76:\t\tcase tea.KeyDown:",
				"77:\t\t\tif m.history.selected < len(m.history.entries)-1 {",
				"83:\t\tcase tea.KeyEnter:",
				"151:\t\tm.appendHistoryMessage(\"user\", text, nil)",
			}, "\n"),
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/surfaces/tui/app/model_state.go lines 133-180",
			Output: strings.Join([]string{
				"133:type Model struct {",
				"166:\tmessages []llama.ChatMessage",
				"237:\thistory historyState",
			}, "\n"),
		},
	}

	got := synthesizeBriefConversationFallback(brief, results)
	if strings.HasPrefix(got, "<<QUESTION>>") {
		t.Fatalf("answerable TUI key-handler evidence should synthesize an answer, got:\n%s", got)
	}
	for _, want := range []string{"internal/surfaces/tui/app/update_handlers.go", "Model.handleKeyPress", "tea.KeyUp", "tea.KeyDown", "appendHistoryMessage"} {
		if !strings.Contains(got, want) {
			t.Fatalf("TUI fallback answer missing %q:\n%s", want, got)
		}
	}
	runner := &Runner{RuntimeBrief: brief}
	if kind, reason := runner.finalAnswerBlockerKind(got, results); kind != "" || reason != "" {
		t.Fatalf("TUI fallback answer blocked: kind=%q reason=%q\n%s", kind, reason, got)
	}

	codeNavigateResults := []workspace.ToolResult{{
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
	got = synthesizeBriefConversationFallback(brief, codeNavigateResults)
	if strings.HasPrefix(got, "<<QUESTION>>") {
		t.Fatalf("answerable code_navigate key-handler evidence should synthesize an answer, got:\n%s", got)
	}
	for _, want := range []string{"internal/surfaces/tui/app/update_handlers.go", "Model.handleKeyPress", "tea.KeyUp", "tea.KeyDown", "appendHistoryMessage"} {
		if !strings.Contains(got, want) {
			t.Fatalf("code_navigate fallback answer missing %q:\n%s", want, got)
		}
	}
	if kind, reason := runner.finalAnswerBlockerKind(got, codeNavigateResults); kind != "" || reason != "" {
		t.Fatalf("code_navigate TUI fallback answer blocked: kind=%q reason=%q\n%s", kind, reason, got)
	}
}

func TestSynthesizeBriefConversationFallbackPrefersObjectiveRelevantExactProbe(t *testing.T) {
	objective := "what would it take to enable the up and down arrow keys to iterate through the user's chat history?"
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		Objective:         objective,
	}
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output: strings.Join([]string{
				"capability: read_symbol",
				"language: go",
				"routed_tool: go_read_symbol",
				"route_summary: Read Model.handleHistoryAudit in internal/surfaces/tui/app/history_audit.go lines 1-8",
				"",
				"1:func (m *Model) handleHistoryAudit() (tea.Cmd, bool) {",
				"2:\tm.recordHistoryAuditEvent()",
				"3:\treturn nil, false",
				"4:}",
			}, "\n"),
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output: strings.Join([]string{
				"capability: read_symbol",
				"language: go",
				"routed_tool: go_read_symbol",
				"route_summary: Read Model.handleKeyPress in internal/surfaces/tui/app/update_handlers.go lines 22-180",
				"",
				"22:func (m *Model) handleKeyPress(msg tea.KeyMsg) (tea.Cmd, bool) {",
				"43:\t\tcase tea.KeyUp:",
				"44:\t\t\tm.iterateChatHistory(-1)",
				"49:\t\tcase tea.KeyDown:",
				"50:\t\t\tm.iterateChatHistory(1)",
				"70:\t\t\tif m.history.selected > 0 {",
				"77:\t\t\tif m.history.selected < len(m.history.entries)-1 {",
				"171:\t\tmessage := m.chatHistory.entries[m.history.selected]",
				"172:\t\tm.input.SetValue(message.Text)",
				"173:\t\tm.appendHistoryMessage(\"user\", message.Text, nil)",
			}, "\n"),
		},
	}

	got := synthesizeBriefConversationFallback(brief, results)
	if !strings.Contains(got, "Model.handleKeyPress") {
		t.Fatalf("expected key handler to win source-owner selection, got:\n%s", got)
	}
	if strings.Contains(got, "Model.handleHistoryAudit") {
		t.Fatalf("off-objective history audit handler should not win source-owner selection:\n%s", got)
	}
}

func TestSynthesizeBriefConversationFallbackDoesNotUseConflictingNavigationOwner(t *testing.T) {
	objective := "what would it take to enable the up and down arrow keys to iterate through the user's chat history?"
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		Objective:         objective,
	}
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output: strings.Join([]string{
				"route_summary: Read Model.handleSessionsPanelKey in internal/surfaces/tui/app/sessions_panel.go lines 205-245",
				"205:// handleSessionsPanelKey consumes a key press while the Sessions tab",
				"206:// has focus. Returns (true, cmd) if handled. j/k navigate, Enter",
				"207:// resumes the selected session.",
				"208:func (m *Model) handleSessionsPanelKey(msg tea.KeyMsg) (bool, tea.Cmd) {",
				"211:\tcase tea.KeyUp:",
				"215:\tcase tea.KeyDown:",
				"230:\t\tif err := m.ResumeSession(id); err != nil {",
				"236:\t\t\t// Bounce the panel back to History so the resumed chat",
			}, "\n"),
		},
	}

	got := synthesizeBriefConversationFallback(brief, results)
	if strings.Contains(got, "It would take") || strings.HasPrefix(got, "<<QUESTION>>") || !strings.Contains(got, "not have enough supported evidence") {
		t.Fatalf("conflicting navigation owner should not synthesize implementation answer or expose blocker protocol:\n%s", got)
	}
}

func TestFinalAnswerDecoratorDoesNotUseDelegatedPacketWithoutImplementationOwner(t *testing.T) {
	objective := "what would it take to enable the up and down arrow keys to iterate through the user's chat history?"
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		Objective:         objective,
	}
	results := offOwnerDelegatedImplementationPacketResults(objective)
	runner := &Runner{}
	got := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     "Partial findings from this turn: delegated packet available.",
		StopReason: "rejection_retry_blocked",
		Brief:      brief,
		Results:    results,
		UserFacing: true,
	})
	if strings.Contains(got, "handleHelpOverlayKey") || strings.Contains(got, "handles up/down") || !strings.Contains(got, "not have enough supported evidence") {
		t.Fatalf("implementation-shape delegated packet without owner evidence should not synthesize answer:\n%s", got)
	}
}

func TestFallbackHelpersDoNotUseDelegatedPacketWithoutImplementationOwner(t *testing.T) {
	objective := "what would it take to enable the up and down arrow keys to iterate through the user's chat history?"
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		Objective:         objective,
	}
	results := offOwnerDelegatedImplementationPacketResults(objective)

	if got, ok := immediateBudgetFallbackAnswer("main", brief, "maximum tool steps reached", results); ok {
		if strings.Contains(got, "handleHelpOverlayKey") || strings.Contains(got, "handles up/down") {
			t.Fatalf("budget fallback should not synthesize off-owner delegated packet:\n%s", got)
		}
	}
	if got, ok := synthesizeNoToolsFallbackAnswer("main", brief, results); ok {
		if strings.Contains(got, "handleHelpOverlayKey") || strings.Contains(got, "handles up/down") {
			t.Fatalf("no-tools fallback should not synthesize off-owner delegated packet:\n%s", got)
		}
	}
}

func TestFallbackHelpersDoNotLeakLegacyDelegatedTaskWithoutImplementationOwner(t *testing.T) {
	objective := "what would it take to enable the up and down arrow keys to iterate through the user's chat history?"
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		Objective:         objective,
	}
	results := []workspace.ToolResult{
		{
			Tool:    "task",
			Summary: "Child task completed",
			Output: strings.Join([]string{
				"Question: " + objective,
				"Inspected paths:",
				"- `internal/surfaces/tui/app/help_overlay.go`",
				"Key findings:",
				"- `Model.handleHelpOverlayKey` handles up/down key movement while the help overlay has focus.",
			}, "\n"),
		},
		{
			Tool:    "search_content",
			Summary: "Found 2 matching line(s).",
			Output:  "internal/surfaces/tui/app/help_overlay.go:44: case tea.KeyUp:\ninternal/surfaces/tui/app/help_overlay.go:47: case tea.KeyDown:",
		},
	}

	for name, call := range map[string]func() (string, bool){
		"budget": func() (string, bool) {
			return immediateBudgetFallbackAnswer("main", brief, "maximum tool steps reached", results)
		},
		"no_tools": func() (string, bool) {
			return synthesizeNoToolsFallbackAnswer("main", brief, results)
		},
	} {
		got, ok := call()
		if !ok {
			continue
		}
		if strings.Contains(got, "handleHelpOverlayKey") || strings.Contains(got, "handles up/down") {
			t.Fatalf("%s fallback leaked off-owner delegated task output:\n%s", name, got)
		}
	}
}

func offOwnerDelegatedImplementationPacketResults(objective string) []workspace.ToolResult {
	return []workspace.ToolResult{typedReadOnlyTaskResult(ReadOnlyEvidencePacket{
		Objective:       objective,
		SourceBodyPaths: []string{"internal/surfaces/tui/app/help_overlay.go"},
		Findings: []ReadOnlyEvidenceFinding{
			{Path: "internal/surfaces/tui/app/help_overlay.go", Text: "`Model.handleHelpOverlayKey` handles up/down key movement while the help overlay has focus."},
		},
		Confidence: "medium",
	})}
}

func TestDecorateFinalAnswerConversationFallbackDoesNotInventBudgetExhaustion(t *testing.T) {
	runner := &Runner{}
	got := runner.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     "Partial findings from this turn (the budget was reached before a full synthesis):\n\nVerified context:\n- read_file internal/workflows/architect/packet.go",
		StopReason: "model_final",
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			Objective:         "Explain internal/workflows/architect/packet.go.",
		},
		Results: []workspace.ToolResult{
			{Tool: "read_file", Summary: "Read file: internal/workflows/architect/packet.go", Output: "type Evidence struct {...}"},
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
	if !strings.Contains(got, "internal/workflows/architect/packet.go") {
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
		{Tool: "read_file", Summary: "Read file: internal/workflows/architect/packet.go", Output: "type Evidence struct {\n\tKind  string `json:\"kind\"`\n\tLabel string `json:\"label,omitempty\"`\n}"},
	}
	internalAnswer := "Partial findings from this turn (the budget was reached before a full synthesis):\n\nVerified context:\n- read_file internal/workflows/architect/packet.go"

	got, ok := presentableNoToolsFallbackAnswer(brief, results, internalAnswer)
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

	got, ok := presentableNoToolsFallbackAnswer(brief, results, internalAnswer)
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
		Summary: "Read f1 internal/evidence/transcript/debuglog.go lines 1-20",
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
	for _, want := range []string{"debug log redacted mode", "internal/evidence/transcript/debuglog.go", "DebugLogModeRedacted", "DebugLogModeFull"} {
		if !strings.Contains(got, want) {
			t.Fatalf("deterministic answer missing %q:\n%s", want, got)
		}
	}
	if strings.HasPrefix(got, "<<QUESTION>>") || strings.Contains(got, "did not retain a concrete user-facing finding") {
		t.Fatalf("concrete objective and source body should not collapse to blocker:\n%s", got)
	}
}

func TestDeterministicNoToolsAnswerCombinesReadFileFactsForRedactedMetadata(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		Objective:         "What does the debug log redacted mode do?",
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/evidence/transcript/debuglog.go lines 1-50",
		Output: strings.Join([]string{
			"1:type DebugLogMode string",
			"2:const DebugLogModeRedacted DebugLogMode = \"redacted\"",
			"3:type PromptSource struct {",
			"4:\tPath string",
			"5:}",
		}, "\n"),
	}, {
		Tool:    "read_file",
		Summary: "Read f1 internal/evidence/transcript/debuglog.go lines 218-240",
		Output: strings.Join([]string{
			"218:func (l *DebugLogger) LogSessionStart(opts DebugLoggerOptions) {",
			"219:\trecord := map[string]any{}",
			"220:\trecord[\"mode\"] = string(l.mode)",
			"221:\trecord[\"prompt_sources\"] = l.sanitizePromptSources(opts.PromptSources)",
			"222:\trecord[\"workspace_root\"] = l.sanitizeText(opts.WorkspaceRoot)",
			"223:\tif l.mode == DebugLogModeFull {",
			"224:\t\trecord[\"system_prompt\"] = opts.SystemPrompt",
			"225:\t} else if digest := promptDigest(opts.SystemPrompt); digest != \"\" {",
			"226:\t\trecord[\"system_prompt_digest\"] = digest",
			"227:\t}",
			"228:}",
		}, "\n"),
	}}

	got, ok := deterministicNoToolsAnswer("main", brief, results)
	if !ok {
		t.Fatal("expected deterministic source-body answer")
	}
	for _, want := range []string{"redacted", "debug log", "internal/evidence/transcript/debuglog.go", "secret", "path", "metadata"} {
		if !strings.Contains(strings.ToLower(got), strings.ToLower(want)) {
			t.Fatalf("deterministic answer missing %q:\n%s", want, got)
		}
	}
}
