package agent

import (
	"strings"
	"testing"

	"idiom/internal/frontier/workerruntime"
	"idiom/internal/workspace"
)

func TestScaleMaxToolStepsByRequiredPathsCurve(t *testing.T) {
	cases := []struct {
		paths    int
		base     int
		want     int
		describe string
	}{
		{0, maxToolStepsBase, maxToolStepsBase, "no required paths leaves base alone"},
		{1, maxToolStepsBase, maxToolStepsBase, "single-file atom uses base"},
		{2, maxToolStepsBase, 18, "5+2*(N-1) for two paths"},
		{5, maxToolStepsBase, 24, "5-file atom gets a meaningful expansion"},
		{12, maxToolStepsBase, 38, "12-file atom near ceiling"},
		{15, maxToolStepsBase, maxToolStepsCeiling, "above-scale atoms cap"},
	}
	for _, tc := range cases {
		policy := RunPolicy{MaxToolSteps: tc.base}
		scaled := scaleMaxToolStepsByRequiredPaths(policy, tc.paths)
		if scaled.MaxToolSteps != tc.want {
			t.Errorf("%s: got MaxToolSteps=%d, want %d", tc.describe, scaled.MaxToolSteps, tc.want)
		}
	}
}

func TestScaleMaxToolStepsLeavesUnboundedExplicitTightAndAlreadyHighPoliciesAlone(t *testing.T) {
	// MaxToolSteps==0 means unbounded; never lower it to a fixed cap.
	if got := scaleMaxToolStepsByRequiredPaths(RunPolicy{MaxToolSteps: 0}, 10); got.MaxToolSteps != 0 {
		t.Fatalf("unbounded policy must stay unbounded, got %d", got.MaxToolSteps)
	}
	// A configured policy already above the scaled value should not be lowered.
	if got := scaleMaxToolStepsByRequiredPaths(RunPolicy{MaxToolSteps: 60}, 10); got.MaxToolSteps != 60 {
		t.Fatalf("explicitly-higher policy must not be lowered, got %d", got.MaxToolSteps)
	}
	// Explicit tight policies (below the base) signal deliberate intent —
	// usually tests forcing budget exhaustion. They must not be raised.
	if got := scaleMaxToolStepsByRequiredPaths(RunPolicy{MaxToolSteps: 1}, 5); got.MaxToolSteps != 1 {
		t.Fatalf("explicit tight policy (below base) must not be raised, got %d", got.MaxToolSteps)
	}
	if got := scaleMaxToolStepsByRequiredPaths(RunPolicy{MaxToolSteps: 4}, 5); got.MaxToolSteps != 4 {
		t.Fatalf("explicit tight policy (below base) must not be raised, got %d", got.MaxToolSteps)
	}
}

func TestConsumeRecoveryReadAllowanceGrantsOnePerRemainingPath(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go", "b.go", "c.go"},
		},
	}
	// One required path already written; remaining = b.go, c.go.
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited a.go"},
	}
	used := map[string]bool{}

	// First read on a remaining path is allowed.
	if !runner.consumeRecoveryReadAllowance(workspace.ToolRequest{Tool: "read_file", Path: "b.go"}, used, results) {
		t.Fatalf("first read on remaining path should be allowed")
	}
	// Second read on the same path is blocked.
	if runner.consumeRecoveryReadAllowance(workspace.ToolRequest{Tool: "read_file", Path: "b.go"}, used, results) {
		t.Fatalf("second read on same remaining path should be blocked")
	}
	// First read on the OTHER remaining path is still allowed.
	if !runner.consumeRecoveryReadAllowance(workspace.ToolRequest{Tool: "read_file", Path: "c.go"}, used, results) {
		t.Fatalf("first read on a different remaining path should be allowed")
	}
	// Read on an already-written path is blocked.
	if runner.consumeRecoveryReadAllowance(workspace.ToolRequest{Tool: "read_file", Path: "a.go"}, used, results) {
		t.Fatalf("read on already-written path should be blocked")
	}
	// Non-read tools are blocked even on remaining paths.
	if runner.consumeRecoveryReadAllowance(workspace.ToolRequest{Tool: "search_content", Path: "b.go"}, used, results) {
		t.Fatalf("search tools must not consume allowance — only read_file")
	}
	// Read on a path outside required edit set is blocked.
	if runner.consumeRecoveryReadAllowance(workspace.ToolRequest{Tool: "read_file", Path: "unrelated.go"}, used, results) {
		t.Fatalf("read on a non-required path should be blocked")
	}
	// Empty path is blocked.
	if runner.consumeRecoveryReadAllowance(workspace.ToolRequest{Tool: "read_file", Path: ""}, used, results) {
		t.Fatalf("empty path should not consume allowance")
	}
}

func TestShouldForceAnswerForPostCompletionVerificationCountsAndCutsOff(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}
	// All required paths written.
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited a.txt"},
		{Tool: "edit_file", Summary: "Edited b.txt"},
	}
	counter := 0
	read := workspace.ToolRequest{Tool: "read_file", Path: "a.txt"}
	// First three calls increment the counter but do not yet trip.
	for i := 1; i <= postCompletionVerificationCutoff; i++ {
		if runner.shouldForceAnswerForPostCompletionVerification(read, results, &counter) {
			t.Fatalf("force-answer fired prematurely on call %d", i)
		}
		if counter != i {
			t.Fatalf("counter want %d, got %d", i, counter)
		}
	}
	// The next non-write call must trip the cutoff.
	if !runner.shouldForceAnswerForPostCompletionVerification(read, results, &counter) {
		t.Fatalf("force-answer should fire after %d non-write calls", postCompletionVerificationCutoff+1)
	}
}

func TestShouldForceAnswerForPostCompletionVerificationWaitsForSourceVerification(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			RequiredEditPaths:    []string{"a.go"},
			VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
		},
	}
	results := []workspace.ToolResult{{Tool: "edit_file", Summary: "Edited a.go"}}
	counter := 0
	read := workspace.ToolRequest{Tool: "read_file", Path: "a.go"}
	for i := 0; i < postCompletionVerificationCutoff*3; i++ {
		if runner.shouldForceAnswerForPostCompletionVerification(read, results, &counter) {
			t.Fatalf("force-answer must not fire while source verification is pending (iter %d)", i)
		}
		if counter != 0 {
			t.Fatalf("pending verification must not increment cutoff counter, got %d", counter)
		}
	}
}

func TestShouldForceAnswerForPostCompletionVerificationWaitsAfterFailedVerification(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			RequiredEditPaths:    []string{"a.go"},
			VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
		},
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited a.go"},
		{Tool: "source_sanity", Summary: "Source sanity failed", Error: "a.go:12: undefined: missingSymbol"},
	}
	counter := 0
	read := workspace.ToolRequest{Tool: "read_file", Path: "a.go"}
	for i := 0; i < postCompletionVerificationCutoff*3; i++ {
		if runner.shouldForceAnswerForPostCompletionVerification(read, results, &counter) {
			t.Fatalf("force-answer must not fire after failed verification (iter %d)", i)
		}
		if counter != 0 {
			t.Fatalf("failed verification must not increment cutoff counter, got %d", counter)
		}
	}
}

func TestShouldForceAnswerForPostCompletionVerificationCountsAfterSuccessfulSourceVerification(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			RequiredEditPaths:    []string{"a.go"},
			VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
		},
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited a.go"},
		{Tool: "source_sanity", Summary: "Source sanity passed", Output: "a.go: no warnings or errors via go_diagnostics"},
	}
	counter := 0
	read := workspace.ToolRequest{Tool: "read_file", Path: "a.go"}
	for i := 1; i <= postCompletionVerificationCutoff; i++ {
		if runner.shouldForceAnswerForPostCompletionVerification(read, results, &counter) {
			t.Fatalf("force-answer fired prematurely on call %d", i)
		}
	}
	if !runner.shouldForceAnswerForPostCompletionVerification(read, results, &counter) {
		t.Fatalf("force-answer should fire after successful source verification and repeated non-write calls")
	}
}

func TestCompletionGateRecoveryPromptAfterFailedVerificationRequiresRepair(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			RequiredEditPaths:    []string{"a.go"},
			VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
		},
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited a.go"},
		{Tool: "source_sanity", Summary: "Source sanity failed", Error: "a.go:12: undefined: missingSymbol"},
	}
	prompt, hideReads, ok := runner.completionGateRecoveryPrompt(results)
	if !ok {
		t.Fatalf("expected failed verification recovery prompt")
	}
	if hideReads {
		t.Fatalf("failed verification repair should keep narrow read/discovery available")
	}
	for _, want := range []string{
		"Post-write verification failed",
		"repair evidence",
		"repair the affected in-scope file",
		"one narrow read/language/context lookup",
		"Rerun verification only after a repair write",
		"<<QUESTION>>",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to contain %q, got:\n%s", want, prompt)
		}
	}
}

func TestRunnerCompactionSummaryPreservesFailedVerificationRepairPacket(t *testing.T) {
	runner := &Runner{
		AgentName: "main",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			RequiredEditPaths:    []string{"a.go"},
			VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
		},
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited a.go"},
		{Tool: "source_sanity", Summary: "Source sanity failed", Error: "a.go:12: undefined: missingSymbol"},
	}
	summary := runner.runnerCompactionSummary(RunPolicy{}, nil, results)
	for _, want := range []string{
		"Open failed verification repair packet",
		"source_sanity",
		"Source sanity failed",
		"undefined: missingSymbol",
		"repair the failing changed/required path",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("expected compaction summary to contain %q, got:\n%s", want, summary)
		}
	}
}

func TestShouldForceAnswerForPostCompletionVerificationIgnoresWrites(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt"},
		},
	}
	results := []workspace.ToolResult{{Tool: "edit_file", Summary: "Edited a.txt"}}
	counter := 0
	// Even at the threshold, writes don't count.
	write := workspace.ToolRequest{Tool: "edit_file", Path: "a.txt"}
	for i := 0; i < postCompletionVerificationCutoff*5; i++ {
		if runner.shouldForceAnswerForPostCompletionVerification(write, results, &counter) {
			t.Fatalf("write tool should never trip the cutoff (iter %d)", i)
		}
		if counter != 0 {
			t.Fatalf("write must not increment the counter, got %d", counter)
		}
	}
}

func TestShouldForceAnswerForPostCompletionVerificationSkipsWhenPathsRemain(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}
	// Only one of two required paths written — verification cutoff shouldn't engage.
	results := []workspace.ToolResult{{Tool: "edit_file", Summary: "Edited a.txt"}}
	counter := 0
	read := workspace.ToolRequest{Tool: "read_file", Path: "b.txt"}
	for i := 0; i < postCompletionVerificationCutoff*5; i++ {
		if runner.shouldForceAnswerForPostCompletionVerification(read, results, &counter) {
			t.Fatalf("cutoff should not engage while required paths remain (iter %d)", i)
		}
		if counter != 0 {
			t.Fatalf("counter must stay at 0 while paths remain, got %d", counter)
		}
	}
}

func TestPreflightApplyPatchSkipsAlreadyChangedSectionAndAppliesRemainder(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt", "c.txt"},
		},
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited a.txt"},
	}
	patch := "*** Begin Patch\n" +
		"*** Update File: a.txt\n" +
		"@@\n" +
		" alpha\n" +
		"-old\n" +
		"+new\n" +
		"*** Update File: b.txt\n" +
		"@@\n" +
		" beta\n" +
		"-old\n" +
		"+new\n" +
		"*** End Patch"
	got := runner.preflightApplyPatchAgainstChangedPaths(
		workspace.ToolRequest{Tool: "apply_patch", Content: patch},
		results,
	)
	if got.Action != applyPatchPreflightRewrite {
		t.Fatalf("expected rewrite action, got %q", got.Action)
	}
	if len(got.SkippedPaths) != 1 || got.SkippedPaths[0] != "a.txt" {
		t.Fatalf("expected a.txt to be skipped, got %v", got.SkippedPaths)
	}
	rewritten := got.Request.Content
	if strings.Contains(rewritten, "*** Update File: a.txt") {
		t.Fatalf("rewritten patch should not contain the redundant a.txt section: %s", rewritten)
	}
	if !strings.Contains(rewritten, "*** Update File: b.txt") {
		t.Fatalf("rewritten patch should retain the b.txt section: %s", rewritten)
	}
	if !strings.HasPrefix(rewritten, "*** Begin Patch") || !strings.Contains(rewritten, "*** End Patch") {
		t.Fatalf("rewritten patch should keep the envelope markers: %s", rewritten)
	}
}

func TestPreflightApplyPatchAllSectionsRedundantSynthesizesSuccess(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt"},
		},
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited a.txt"},
		{Tool: "edit_file", Summary: "Edited b.txt"},
	}
	// Every section targets a path that has already been changed.
	patch := "*** Begin Patch\n" +
		"*** Update File: a.txt\n" +
		"@@\n" +
		" alpha\n" +
		"-old\n" +
		"+new\n" +
		"*** Update File: b.txt\n" +
		"@@\n" +
		" beta\n" +
		"-old\n" +
		"+new\n" +
		"*** End Patch"
	got := runner.preflightApplyPatchAgainstChangedPaths(
		workspace.ToolRequest{Tool: "apply_patch", Content: patch},
		results,
	)
	if got.Action != applyPatchPreflightAllSkipped {
		t.Fatalf("expected all_skipped action, got %q", got.Action)
	}
	if got.Result.Error != "" {
		t.Fatalf("synthesized result should not carry an error, got %q", got.Result.Error)
	}
	if !strings.Contains(got.Result.Summary, "no new changes") {
		t.Fatalf("synthesized result summary should clearly state no new changes, got %q", got.Result.Summary)
	}
	if len(got.SkippedPaths) != 2 {
		t.Fatalf("expected both paths in skipped list, got %v", got.SkippedPaths)
	}
}

func TestPreflightApplyPatchAgainstChangedPathsPassesCleanPatch(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt", "c.txt"},
		},
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited a.txt"},
	}
	// All sections target paths that have NOT been changed yet.
	patch := "*** Begin Patch\n" +
		"*** Update File: b.txt\n" +
		"@@\n" +
		" beta\n" +
		"-old\n" +
		"+new\n" +
		"*** Update File: c.txt\n" +
		"@@\n" +
		" gamma\n" +
		"-old\n" +
		"+new\n" +
		"*** End Patch"
	got := runner.preflightApplyPatchAgainstChangedPaths(
		workspace.ToolRequest{Tool: "apply_patch", Content: patch},
		results,
	)
	if got.Action != applyPatchPreflightPassthrough {
		t.Fatalf("clean patch should pass through, got %q action", got.Action)
	}
}

func TestPreflightApplyPatchAgainstChangedPathsIgnoresParseErrors(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{RequiredEditPaths: []string{"a.txt"}},
	}
	results := []workspace.ToolResult{{Tool: "edit_file", Summary: "Edited a.txt"}}
	// Malformed patch — the parse helper should let the real applyPatch
	// surface the actual envelope error rather than the pre-flight
	// short-circuiting it.
	bad := "not a real patch"
	got := runner.preflightApplyPatchAgainstChangedPaths(
		workspace.ToolRequest{Tool: "apply_patch", Content: bad},
		results,
	)
	if got.Action != applyPatchPreflightPassthrough {
		t.Fatalf("parse-time errors should fall through to real applyPatch, got action %q", got.Action)
	}
}

func TestPreflightApplyPatchAllSkippedSummaryDiscouragesCompletion(t *testing.T) {
	// When the pre-flight short-circuits with "all sections redundant",
	// the synthesized result must NOT read like the patch did productive
	// work. The model must see (a) "no new changes" and (b) the still-
	// pending required paths so it can't claim completion.
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt", "c.txt"},
		},
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited a.txt"},
	}
	patch := "*** Begin Patch\n" +
		"*** Update File: a.txt\n" +
		"@@\n" +
		" alpha\n" +
		"-old\n" +
		"+new\n" +
		"*** End Patch"
	got := runner.preflightApplyPatchAgainstChangedPaths(
		workspace.ToolRequest{Tool: "apply_patch", Content: patch},
		results,
	)
	if got.Action != applyPatchPreflightAllSkipped {
		t.Fatalf("expected all_skipped, got %q", got.Action)
	}
	if !strings.Contains(got.Result.Summary, "no new changes") {
		t.Fatalf("summary must say no new changes, got %q", got.Result.Summary)
	}
	if !strings.Contains(got.Result.Output, "NOT yet fulfilled") {
		t.Fatalf("output must explicitly say contract not fulfilled, got %q", got.Result.Output)
	}
	if !strings.Contains(got.Result.Output, "b.txt") || !strings.Contains(got.Result.Output, "c.txt") {
		t.Fatalf("output must list pending required paths so the model has a directional next step, got %q", got.Result.Output)
	}
}

func TestRequiredEditPathsProgressMessageRendersCheckmarks(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt", "c.txt"},
		},
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited a.txt"},
	}
	msg := runner.requiredEditPathsProgressMessage(
		workspace.ToolRequest{Tool: "edit_file", Path: "a.txt"},
		workspace.ToolResult{Tool: "edit_file", Summary: "Edited a.txt"},
		results,
	)
	if msg == "" {
		t.Fatalf("expected progress message after successful write on required path")
	}
	if !strings.Contains(msg, "✓ a.txt") {
		t.Fatalf("expected check on a.txt, got %q", msg)
	}
	if !strings.Contains(msg, "✗ b.txt") || !strings.Contains(msg, "✗ c.txt") {
		t.Fatalf("expected x marks on pending paths, got %q", msg)
	}
	if !strings.Contains(msg, "2 required edit paths remain") {
		t.Fatalf("expected pluralized remaining count, got %q", msg)
	}
}

func TestRequiredEditPathsProgressMessageSilentWhenNoRequiredPaths(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	results := []workspace.ToolResult{{Tool: "edit_file", Summary: "Edited foo.txt"}}
	msg := runner.requiredEditPathsProgressMessage(
		workspace.ToolRequest{Tool: "edit_file", Path: "foo.txt"},
		workspace.ToolResult{Tool: "edit_file", Summary: "Edited foo.txt"},
		results,
	)
	if msg != "" {
		t.Fatalf("no required_edit_paths means no progress message; got %q", msg)
	}
}

func TestRequiredEditPathsProgressMessageSilentOnFailedWrite(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt"},
		},
	}
	results := []workspace.ToolResult{{Tool: "edit_file", Error: "no match"}}
	msg := runner.requiredEditPathsProgressMessage(
		workspace.ToolRequest{Tool: "edit_file", Path: "a.txt"},
		workspace.ToolResult{Tool: "edit_file", Error: "no match"},
		results,
	)
	if msg != "" {
		t.Fatalf("failed write must not trigger progress message; got %q", msg)
	}
}

func TestRequiredEditPathsProgressMessageOnFinalCompletion(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt"},
		},
	}
	results := []workspace.ToolResult{{Tool: "edit_file", Summary: "Edited a.txt"}}
	msg := runner.requiredEditPathsProgressMessage(
		workspace.ToolRequest{Tool: "edit_file", Path: "a.txt"},
		workspace.ToolResult{Tool: "edit_file", Summary: "Edited a.txt"},
		results,
	)
	if !strings.Contains(msg, "All required edit paths are written") {
		t.Fatalf("expected completion-state message when last path lands, got %q", msg)
	}
}

func TestFinalAnswerBlockerErrorTextNamesMissingPaths(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.txt", "b.txt", "c.txt"},
		},
	}
	results := []workspace.ToolResult{{Tool: "edit_file", Summary: "Edited a.txt"}}
	answer := "Implementation Complete. Changed paths: a.txt, b.txt, c.txt."
	text := runner.finalAnswerBlockerErrorText("required_edit_paths_remaining", "required_edit_paths_remaining", answer, results)
	if !strings.Contains(text, "b.txt") || !strings.Contains(text, "c.txt") {
		t.Fatalf("error text must name missing required paths, got %q", text)
	}
	if strings.Contains(text, "edit_file") || strings.Contains(text, "apply_patch") {
		t.Fatalf("error text should use capability language instead of raw write-tool prescriptions, got %q", text)
	}
	if !strings.Contains(text, "remaining-path write contract") {
		t.Fatalf("error text should name the write contract for recovery, got %q", text)
	}
}

func TestConsumeRecoveryReadAllowanceNoRemainingPaths(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			RequiredEditPaths: []string{"a.go"},
		},
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited a.go"},
	}
	used := map[string]bool{}
	if runner.consumeRecoveryReadAllowance(workspace.ToolRequest{Tool: "read_file", Path: "a.go"}, used, results) {
		t.Fatalf("with no remaining paths, read should never be allowed")
	}
}
