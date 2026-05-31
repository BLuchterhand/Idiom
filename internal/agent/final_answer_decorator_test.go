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
	for _, want := range []string{"internal/labels/labels.go", "ShortLabel", "\"Save changes\"", "No files were changed"} {
		if !strings.Contains(got, want) {
			t.Fatalf("decorated answer missing %q:\n%s", want, got)
		}
	}
	for _, forbidden := range []string{"Changing that returned string", "would be the source edit"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("decorated answer should not infer edit advice %q:\n%s", forbidden, got)
		}
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
	for _, want := range []string{"At the source level, yes", "internal/labels/labels.go", "\"Save changes\"", "No files were changed"} {
		if !strings.Contains(got, want) {
			t.Fatalf("decorated answer missing %q:\n%s", want, got)
		}
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
	for _, want := range []string{"internal/labels/labels.go", "ShortLabel", "\"Save changes\"", "No files were changed"} {
		if !strings.Contains(got, want) {
			t.Fatalf("decorated answer missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "tool: read_file") || strings.Contains(got, `{"tool"`) {
		t.Fatalf("decorated answer leaked internal tool request:\n%s", got)
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

func TestPresentableNoToolsFallbackUsesSearchEvidenceBody(t *testing.T) {
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
		t.Fatal("expected presentable fallback answer")
	}
	for _, want := range []string{"internal/labels/labels.go", "\"Save changes\"", "No files were changed"} {
		if !strings.Contains(got, want) {
			t.Fatalf("fallback answer missing %q:\n%s", want, got)
		}
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
	results := []workspace.ToolResult{}
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
		{Tool: "context_query", Summary: "Context query returned 12 nodes"},
	}
	got := synthesizeBriefConversationFallback(brief, results)
	if !strings.Contains(got, "inspected") {
		t.Errorf("fallback should mention inspected paths: %q", got)
	}
	if strings.Contains(got, "No files were changed") {
		t.Errorf("fallback should not contain source-body extraction garbage: %q", got)
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
	if !strings.Contains(got, "inspected") {
		t.Errorf("fallback should provide useful context: %q", got)
	}
}
