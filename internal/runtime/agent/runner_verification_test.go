package agent

import (
	"strings"
	"testing"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/workerruntime"
)

func TestLatestVerificationAfterLatestSourceWriteResetsOnLaterSourceWrite(t *testing.T) {
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited internal/surfaces/tui/app/first.go"},
		{Tool: "source_sanity", Summary: "source sanity passed"},
		{Tool: "edit_file", Summary: "Edited internal/surfaces/tui/app/second.go"},
	}

	if latest, ok := latestVerificationAfterLatestSourceWrite(results); ok {
		t.Fatalf("verification before a later source write should not count; got %s", latest.Tool)
	}
}

func TestSourceEditsNeedSemanticVerificationAfterPatchHygieneOnly(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:           "implementation",
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited internal/surfaces/tui/app/app.go"},
		{Tool: "shell", Summary: "Ran shell command", Output: "$ git diff --check -- internal/surfaces/tui/app/app.go\n"},
	}

	if !sourceEditsNeedSemanticVerification(brief, results) {
		t.Fatal("patch hygiene alone should leave source edits needing semantic verification")
	}
	if sourceEditsAreSemanticallyVerified(brief, results) {
		t.Fatal("patch hygiene alone should not satisfy source edit verification")
	}
}

func TestSourceEditsAreSemanticallyVerifiedAfterCompilerBackedSourceSanity(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:           "implementation",
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited internal/surfaces/tui/app/app.go"},
		{Tool: "shell", Summary: "Ran shell command", Output: "$ git diff --check -- internal/surfaces/tui/app/app.go\n"},
		{Tool: "source_sanity", Summary: "source sanity passed", Output: "internal/surfaces/tui/app/app.go: no warnings or errors via go_diagnostics"},
	}

	latest, ok := latestVerificationAfterLatestSourceWrite(results)
	if !ok {
		t.Fatal("expected source_sanity to be the latest verification after source write")
	}
	if latest.Tool != "source_sanity" {
		t.Fatalf("latest verification: want source_sanity, got %s", latest.Tool)
	}
	if sourceEditsNeedSemanticVerification(brief, results) {
		t.Fatal("compiler-backed source_sanity should satisfy source edit semantic verification")
	}
	if !sourceEditsAreSemanticallyVerified(brief, results) {
		t.Fatal("compiler-backed source_sanity should mark source edits as semantically verified")
	}
}

func TestSourceEditsStillNeedVerificationAfterGenericSourceSanityFallback(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:           "implementation",
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited internal/surfaces/tui/app/app.go"},
		{Tool: "source_sanity", Summary: "Source sanity passed for 1 file(s) (1 generic fallback)", Output: "internal/surfaces/tui/app/app.go: generic delimiter sanity passed via generic_source_sanity"},
	}

	if _, ok := latestVerificationAfterLatestSourceWrite(results); ok {
		t.Fatal("generic source_sanity fallback must not count as source verification")
	}
	if !sourceEditsNeedSemanticVerification(brief, results) {
		t.Fatal("generic source_sanity fallback should leave source edits needing language-aware verification")
	}
	if sourceEditsAreSemanticallyVerified(brief, results) {
		t.Fatal("generic source_sanity fallback should not mark source edits as semantically verified")
	}
}

func TestSourceEditsAreSemanticallyVerifiedAfterLanguageCommand(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:           "implementation",
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited internal/surfaces/tui/app/app.go"},
		{Tool: "shell", Summary: "Ran shell command", Output: "$ npm run build\nbuild succeeded"},
	}

	if sourceEditsNeedSemanticVerification(brief, results) {
		t.Fatal("language/project verification command should satisfy source edit verification")
	}
	if !sourceEditsAreSemanticallyVerified(brief, results) {
		t.Fatal("language/project verification command should mark source edits as semantically verified")
	}
}

func TestRewriteVerificationShellReadToSourceSanityForPlannerOwnedChangedSource(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:           "implementation",
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
		Constraints:          []string{"planner_owned_verification"},
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited internal/labels/labels.go"},
	}

	req, ok := rewriteVerificationShellReadToSourceSanity(
		workspace.ToolRequest{Tool: "shell", Command: "cat internal/labels/labels.go"},
		brief,
		results,
		[]string{"source_sanity", "shell"},
	)
	if !ok {
		t.Fatal("expected shell cat of changed source to rewrite to source_sanity")
	}
	if req.Tool != "source_sanity" || req.Path != "internal/labels/labels.go" {
		t.Fatalf("unexpected rewritten request: %#v", req)
	}
}

func TestRewriteVerificationShellReadToSourceSanityRequiresAllowedToolAndChangedPath(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:           "implementation",
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
		Constraints:          []string{"planner_owned_verification"},
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited internal/labels/labels.go"},
	}

	if _, ok := rewriteVerificationShellReadToSourceSanity(
		workspace.ToolRequest{Tool: "shell", Command: "cat internal/labels/labels.go"},
		brief,
		results,
		[]string{"shell"},
	); ok {
		t.Fatal("must not rewrite when source_sanity is not currently allowed")
	}
	if _, ok := rewriteVerificationShellReadToSourceSanity(
		workspace.ToolRequest{Tool: "shell", Command: "cat internal/other.go"},
		brief,
		results,
		[]string{"source_sanity", "shell"},
	); ok {
		t.Fatal("must not rewrite shell reads for unchanged source paths")
	}
}

func TestRewriteVerificationShellReadDoesNotPickSourceSanityForNormalImplementation(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:           "implementation",
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited internal/labels/labels.go"},
	}

	if _, ok := rewriteVerificationShellReadToSourceSanity(
		workspace.ToolRequest{Tool: "shell", Command: "cat internal/labels/labels.go"},
		brief,
		results,
		[]string{"source_sanity", "shell"},
	); ok {
		t.Fatal("normal implementation verification should not have the harness choose source_sanity")
	}
}

func TestFillSourceSanityPathFromSingleChangedSource(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:           "implementation",
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited internal/labels/labels.go"},
	}

	redirected, ok := fillSourceSanityPathFromSingleChangedSource(
		workspace.ToolRequest{Tool: "source_sanity"},
		brief,
		results,
		[]string{"source_sanity", "shell"},
	)
	if !ok {
		t.Fatal("expected pathless source_sanity to inherit the single changed source path")
	}
	if redirected.Tool != "source_sanity" || redirected.Path != "internal/labels/labels.go" {
		t.Fatalf("unexpected source_sanity repair: %#v", redirected)
	}
}

func TestFillSourceSanityPathFromSingleChangedSourceRequiresUnambiguousTarget(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:           "implementation",
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited internal/labels/labels.go"},
		{Tool: "edit_file", Summary: "Edited internal/labels/other.go"},
	}

	if _, ok := fillSourceSanityPathFromSingleChangedSource(
		workspace.ToolRequest{Tool: "source_sanity"},
		brief,
		results,
		[]string{"source_sanity", "shell"},
	); ok {
		t.Fatal("must not choose between multiple changed source paths")
	}
	if _, ok := fillSourceSanityPathFromSingleChangedSource(
		workspace.ToolRequest{Tool: "source_sanity"},
		brief,
		results[:1],
		[]string{"shell"},
	); ok {
		t.Fatal("must not repair when source_sanity is unavailable")
	}
}

func TestPostWriteSourceSanityRedirectsPlannerOwnedReadOfChangedSource(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:           "implementation",
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
		Constraints:          []string{"planner_owned_verification"},
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited internal/labels/labels.go"},
	}

	redirected, ok := postWriteSourceSanityRedirect(
		workspace.ToolRequest{Tool: "read_file", Path: "internal/labels/labels.go"},
		brief,
		results,
		[]string{"source_sanity", "shell"},
	)
	if !ok {
		t.Fatal("expected read_file of changed source to redirect to source_sanity")
	}
	if redirected.Tool != "source_sanity" || redirected.Path != "internal/labels/labels.go" {
		t.Fatalf("unexpected redirect: %#v", redirected)
	}
	redirected, ok = postWriteSourceSanityRedirect(
		workspace.ToolRequest{Tool: "edit_file", Path: "internal/labels/labels.go", OldText: "Save changes", NewText: "Save"},
		brief,
		results,
		[]string{"source_sanity", "shell"},
	)
	if !ok {
		t.Fatal("expected verification-phase write retry on changed source to redirect to source_sanity")
	}
	if redirected.Tool != "source_sanity" || redirected.Path != "internal/labels/labels.go" {
		t.Fatalf("unexpected write redirect: %#v", redirected)
	}
}

func TestPostWriteSourceSanityRedirectRequiresPlannerOwnedPendingSourceVerification(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:           "implementation",
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
		Constraints:          []string{"planner_owned_verification"},
	}
	verified := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited internal/labels/labels.go"},
		{Tool: "source_sanity", Summary: "source sanity passed", Output: "internal/labels/labels.go: no warnings or errors via go_diagnostics"},
	}
	if _, ok := postWriteSourceSanityRedirect(
		workspace.ToolRequest{Tool: "read_file", Path: "internal/labels/labels.go"},
		brief,
		verified,
		[]string{"source_sanity"},
	); ok {
		t.Fatal("must not redirect after source_sanity has already satisfied source verification")
	}
	if _, ok := postWriteSourceSanityRedirect(
		workspace.ToolRequest{Tool: "read_file", Path: "internal/labels/labels.go"},
		brief,
		[]workspace.ToolResult{{Tool: "edit_file", Summary: "Edited internal/labels/labels.go"}},
		[]string{"shell"},
	); ok {
		t.Fatal("must not redirect when source_sanity is not currently allowed")
	}
}

func TestPostWriteSourceSanityRedirectsNormalImplementationReadOfChangedSource(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:           "implementation",
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited internal/labels/labels.go"},
	}

	redirected, ok := postWriteSourceSanityRedirect(
		workspace.ToolRequest{Tool: "read_file", Path: "internal/labels/labels.go"},
		brief,
		results,
		[]string{"source_sanity", "shell"},
	)
	if !ok {
		t.Fatal("expected changed-source read in verification phase to redirect to source_sanity")
	}
	if redirected.Tool != "source_sanity" || redirected.Path != "internal/labels/labels.go" {
		t.Fatalf("unexpected redirect: %#v", redirected)
	}
	if _, ok := postWriteSourceSanityRedirect(
		workspace.ToolRequest{Tool: "read_file", Path: "internal/other.go"},
		brief,
		results,
		[]string{"source_sanity", "shell"},
	); ok {
		t.Fatal("must not redirect normal implementation reads for unchanged source paths")
	}
}

func TestPostWriteSourceSanityRedirectRecoversNormalImplementationAfterInvalidVerificationTool(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:           "implementation",
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited internal/labels/labels.go"},
		{
			Tool:    "edit_file",
			Summary: "Tool not available in current runtime contract",
			Error:   "edit_file is not in the current allowed tool surface. Available tools: source_sanity, git_status, git_diff, shell.",
		},
	}

	redirected, ok := postWriteSourceSanityRedirect(
		workspace.ToolRequest{Tool: "read_file", Path: "internal/labels/labels.go"},
		brief,
		results,
		[]string{"source_sanity", "shell"},
	)
	if !ok {
		t.Fatal("expected repeated invalid verification-phase tool to recover through source_sanity")
	}
	if redirected.Tool != "source_sanity" || redirected.Path != "internal/labels/labels.go" {
		t.Fatalf("unexpected redirect: %#v", redirected)
	}

	redirected, ok = postWriteSourceSanityRedirect(
		workspace.ToolRequest{Tool: "edit_file", Path: "internal/labels/labels.go", OldText: "Save changes", NewText: "Save"},
		brief,
		results,
		[]string{"source_sanity", "shell"},
	)
	if !ok {
		t.Fatal("expected repeated invalid verification-phase write to recover through source_sanity")
	}
	if redirected.Tool != "source_sanity" || redirected.Path != "internal/labels/labels.go" {
		t.Fatalf("unexpected write redirect: %#v", redirected)
	}
}

func TestPostWriteSourceSanityRedirectIgnoresWriteForUnchangedSource(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:           "implementation",
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited internal/labels/labels.go"},
	}

	if _, ok := postWriteSourceSanityRedirect(
		workspace.ToolRequest{Tool: "edit_file", Path: "internal/other.go", OldText: "old", NewText: "new"},
		brief,
		results,
		[]string{"source_sanity", "shell"},
	); ok {
		t.Fatal("verification-phase writes for unchanged source paths must not redirect to source_sanity")
	}
}

func TestSourceVerificationDoesNotTreatIncidentalPathMentionAsVerificationException(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:           "implementation",
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
	}
	results := []workspace.ToolResult{
		{
			Tool:   "read_file",
			Output: "Implement the feature by changing pkg/feature.go and pkg/feature_test.go.",
		},
		{Tool: "edit_file", Summary: "Edited pkg/feature.go"},
		{Tool: "shell", Summary: "git diff --check passed", Output: "git diff --check passed"},
	}

	if summary, errorText, reject := sourceEditVerificationRejection(workspace.ToolRequest{
		Tool: "read_file",
		Path: "pkg/feature_test.go",
	}, brief, results); !reject {
		t.Fatal("incidental source text should not become a verification-phase read exception")
	} else if !strings.Contains(summary, "Source edit verification required") || !strings.Contains(errorText, "source edits are present") {
		t.Fatalf("unexpected verification rejection: %q %q", summary, errorText)
	}
}

func TestVerifiedSourceCloseoutIgnoresIncidentalPathMentions(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:           "implementation",
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
	}
	results := []workspace.ToolResult{
		{
			Tool:   "read_file",
			Output: "Implement the feature by changing pkg/feature.go and pkg/feature_test.go.",
		},
		{Tool: "edit_file", Summary: "Edited pkg/feature.go"},
		{Tool: "shell", Summary: "Ran shell command", Output: "$ cargo check\nFinished dev profile"},
	}

	if prompt, ok := sourceEditCloseoutPrompt(workspace.ToolRequest{
		Tool: "read_file",
		Path: "pkg/feature_test.go",
	}, brief, results); !ok {
		t.Fatal("verified source closeout should not be blocked by incidental path mentions")
	} else if !strings.Contains(prompt, "Successful source edits") {
		t.Fatalf("unexpected closeout prompt:\n%s", prompt)
	}
}
