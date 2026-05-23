package agent

import (
	"strings"
	"testing"

	"idiom/internal/frontier/workerruntime"
	"idiom/internal/workspace"
)

func TestLatestVerificationAfterLatestSourceWriteResetsOnLaterSourceWrite(t *testing.T) {
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited internal/app/first.go"},
		{Tool: "source_sanity", Summary: "source sanity passed"},
		{Tool: "edit_file", Summary: "Edited internal/app/second.go"},
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
		{Tool: "edit_file", Summary: "Edited internal/app/app.go"},
		{Tool: "shell", Summary: "Ran shell command", Output: "$ git diff --check -- internal/app/app.go\n"},
	}

	if !sourceEditsNeedSemanticVerification(brief, results) {
		t.Fatal("patch hygiene alone should leave source edits needing semantic verification")
	}
	if sourceEditsAreSemanticallyVerified(brief, results) {
		t.Fatal("patch hygiene alone should not satisfy source edit verification")
	}
}

func TestSourceEditsAreSemanticallyVerifiedAfterSourceSanity(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:           "implementation",
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited internal/app/app.go"},
		{Tool: "shell", Summary: "Ran shell command", Output: "$ git diff --check -- internal/app/app.go\n"},
		{Tool: "source_sanity", Summary: "source sanity passed"},
	}

	latest, ok := latestVerificationAfterLatestSourceWrite(results)
	if !ok {
		t.Fatal("expected source_sanity to be the latest verification after source write")
	}
	if latest.Tool != "source_sanity" {
		t.Fatalf("latest verification: want source_sanity, got %s", latest.Tool)
	}
	if sourceEditsNeedSemanticVerification(brief, results) {
		t.Fatal("source_sanity should satisfy source edit semantic verification")
	}
	if !sourceEditsAreSemanticallyVerified(brief, results) {
		t.Fatal("source_sanity should mark source edits as semantically verified")
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
		{Tool: "source_sanity", Summary: "source sanity passed"},
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
