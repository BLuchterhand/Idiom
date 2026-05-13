package agent

import (
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
