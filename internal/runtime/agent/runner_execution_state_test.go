package agent

import (
	"strings"
	"testing"

	"idiom/internal/capabilities/workspace"
)

func TestRunnerExecutionStateApplyResultVerificationResetsVerificationGuards(t *testing.T) {
	state := newRunnerExecutionState(false, 0)
	state.writesSinceVerification = 3
	budget := newRunnerRequestBudgetState(2)
	budget.readCallsSinceWrite = 4

	req := workspace.ToolRequest{Tool: "shell", Command: "go test ./..."}
	result := workspace.ToolResult{
		Tool:    "shell",
		Summary: "go test failed",
		Error:   "exit status 1",
	}

	update := state.applyResult(req, result, "", nil, &budget)

	if !state.verificationAttempted {
		t.Fatalf("verificationAttempted = false, want true")
	}
	if state.writesSinceVerification != 0 {
		t.Fatalf("writesSinceVerification = %d, want 0", state.writesSinceVerification)
	}
	if !update.ResetVerificationGuards {
		t.Fatalf("ResetVerificationGuards = false, want true")
	}
	if update.VerificationState != "failed" {
		t.Fatalf("VerificationState = %q, want failed", update.VerificationState)
	}
	if update.VerificationDetail != "exit status 1" {
		t.Fatalf("VerificationDetail = %q, want exit status 1", update.VerificationDetail)
	}
	if budget.readCallsSinceWrite != 0 {
		t.Fatalf("readCallsSinceWrite = %d, want 0", budget.readCallsSinceWrite)
	}
}

func TestVerificationPhaseStateMarksGenericSourceSanityInsufficient(t *testing.T) {
	result := workspace.ToolResult{
		Tool:    "source_sanity",
		Summary: "Source sanity passed for 1 file(s) (1 generic fallback)",
		Output:  "internal/labels/labels.go: generic delimiter sanity passed via generic_source_sanity",
	}

	state, detail := verificationPhaseState(result)
	if state != "insufficient" {
		t.Fatalf("state = %q, want insufficient", state)
	}
	if !strings.Contains(detail, "insufficient") {
		t.Fatalf("detail should explain insufficiency, got %q", detail)
	}
}

func TestVerificationPhaseStateSeparatesPatchHygieneFromPassedVerification(t *testing.T) {
	result := workspace.ToolResult{
		Tool:    "shell",
		Summary: "Ran shell command",
		Output:  "$ git diff --check -- internal/labels/labels.go\n",
	}

	state, detail := verificationPhaseState(result)
	if state != "hygiene" {
		t.Fatalf("state = %q, want hygiene", state)
	}
	if !strings.Contains(detail, "patch hygiene") {
		t.Fatalf("detail should identify patch hygiene, got %q", detail)
	}
}

func TestRunnerExecutionStateApplyResultWriteProgressUpdatesCounters(t *testing.T) {
	state := newRunnerExecutionState(false, 0)
	budget := newRunnerRequestBudgetState(2)
	budget.readCallsSinceWrite = 5

	req := workspace.ToolRequest{Tool: "edit_file", Path: "internal/runtime/agent/runner.go"}
	result := workspace.ToolResult{
		Tool:    "edit_file",
		Summary: "Edited internal/runtime/agent/runner.go",
	}

	update := state.applyResult(req, result, "", nil, &budget)

	if update.ResetVerificationGuards {
		t.Fatalf("ResetVerificationGuards = true, want false")
	}
	if state.writesSinceVerification != 1 {
		t.Fatalf("writesSinceVerification = %d, want 1", state.writesSinceVerification)
	}
	if budget.readCallsSinceWrite != 0 {
		t.Fatalf("readCallsSinceWrite = %d, want 0", budget.readCallsSinceWrite)
	}
}

func TestRunnerExecutionStateFailureTrackingAndMissingPathProbe(t *testing.T) {
	state := newRunnerExecutionState(false, 0)

	readReq := workspace.ToolRequest{Tool: "read_file", Path: "missing/foo.go"}
	readResult := workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read failed",
		Error:   "missing",
	}
	if got := state.noteToolFailure(readReq, readResult); got != 1 {
		t.Fatalf("first noteToolFailure = %d, want 1", got)
	}
	if got := state.noteToolFailure(readReq, readResult); got != 2 {
		t.Fatalf("second noteToolFailure = %d, want 2", got)
	}
	if got := state.noteToolFailure(readReq, workspace.ToolResult{Tool: "read_file", Summary: "Read ok", Output: "ok"}); got != 0 {
		t.Fatalf("success noteToolFailure = %d, want 0", got)
	}

	listReq := workspace.ToolRequest{Tool: "list_files", Path: "missing"}
	listResult := workspace.ToolResult{
		Tool:    "list_files",
		Summary: "List failed",
		Error:   "no such file or directory",
	}
	if got := state.noteMissingPathListFailure(true, listReq, listResult); got != 1 {
		t.Fatalf("first noteMissingPathListFailure = %d, want 1", got)
	}
	if got := state.noteMissingPathListFailure(true, listReq, listResult); got != 2 {
		t.Fatalf("second noteMissingPathListFailure = %d, want 2", got)
	}
	if got := state.noteMissingPathListFailure(false, listReq, listResult); got != 0 {
		t.Fatalf("reset noteMissingPathListFailure = %d, want 0", got)
	}
}

func TestRunnerExecutionStateTracksPendingSearchInspection(t *testing.T) {
	state := newRunnerExecutionState(false, 0)

	searchReq := workspace.ToolRequest{Tool: "search_files", Path: "internal", Pattern: "*runner*"}
	searchResult := workspace.ToolResult{
		Tool:    "search_files",
		Summary: "Found 2 matching file(s).",
		Output:  "f1 internal/runtime/agent/runner.go\nf2 internal/runtime/agent/runner_child_tasks.go",
	}
	state.applyResult(searchReq, searchResult, "", nil, nil)

	if !state.pendingSearchInspection {
		t.Fatal("expected pendingSearchInspection after successful search hit")
	}
	if state.shouldConsumeSearchHitBeforeMoreSearch(workspace.ToolRequest{Tool: "search_files", Path: ".", Pattern: "**/*.go"}) {
		t.Fatal("expected broad follow-up file search to remain available; exact-repeat guards own loop prevention")
	}
	if state.shouldConsumeSearchHitBeforeMoreSearch(workspace.ToolRequest{Tool: "search_files", Path: ".", Pattern: "**/input*.go"}) {
		t.Fatal("expected concrete follow-up file search to remain available after successful search hit")
	}
	if state.shouldConsumeSearchHitBeforeMoreSearch(workspace.ToolRequest{Tool: "search_content", Path: "internal/runtime/agent/runner.go", Pattern: "budget"}) {
		t.Fatal("expected file-scoped follow-up content search to remain available after successful search hit")
	}
	if state.shouldConsumeSearchHitBeforeMoreSearch(workspace.ToolRequest{Tool: "search_content", Path: ".", Pattern: "budget runner"}) {
		t.Fatal("expected repo-root follow-up content search to remain available; exact-repeat guards own loop prevention")
	}
	searchFeedback := state.searchHitConsumptionError()
	for _, want := range []string{
		"Returned hit(s):",
		"internal/runtime/agent/runner.go",
		"internal/runtime/agent/runner_child_tasks.go",
	} {
		if !strings.Contains(searchFeedback, want) {
			t.Fatalf("search feedback missing %q:\n%s", want, searchFeedback)
		}
	}

	readReq := workspace.ToolRequest{Tool: "read_file", Path: "internal/runtime/agent/runner.go"}
	readResult := workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read internal/runtime/agent/runner.go lines 1-20",
		Output:  "package agent\n",
	}
	state.applyResult(readReq, readResult, "", nil, nil)

	if state.pendingSearchInspection {
		t.Fatal("expected concrete inspection to clear pending search state")
	}
}
