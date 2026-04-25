package agent

import (
	"testing"

	"idiom/internal/workspace"
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

func TestRunnerExecutionStateApplyResultWriteProgressUpdatesCounters(t *testing.T) {
	state := newRunnerExecutionState(false, 0)
	budget := newRunnerRequestBudgetState(2)
	budget.readCallsSinceWrite = 5

	req := workspace.ToolRequest{Tool: "edit_file", Path: "internal/agent/runner.go"}
	result := workspace.ToolResult{
		Tool:    "edit_file",
		Summary: "Edited internal/agent/runner.go",
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
		Output:  "f1 internal/agent/runner.go\nf2 internal/agent/runner_child_tasks.go",
	}
	state.applyResult(searchReq, searchResult, "", nil, nil)

	if !state.pendingSearchInspection {
		t.Fatal("expected pendingSearchInspection after successful search hit")
	}
	if !state.shouldConsumeSearchHitBeforeMoreSearch(workspace.ToolRequest{Tool: "search_files", Path: "internal", Pattern: "*task*"}) {
		t.Fatal("expected follow-up search to be blocked until a concrete inspection happens")
	}

	readReq := workspace.ToolRequest{Tool: "read_file", Path: "internal/agent/runner.go"}
	readResult := workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read internal/agent/runner.go lines 1-20",
		Output:  "package agent\n",
	}
	state.applyResult(readReq, readResult, "", nil, nil)

	if state.pendingSearchInspection {
		t.Fatal("expected concrete inspection to clear pending search state")
	}
}
