package agent

import (
	"strings"
	"testing"

	"idiom/internal/capabilities/workspace"
)

func TestTaskLedgerRejectsNewCompletedItemWithoutEvidenceWhenRequired(t *testing.T) {
	state := newTaskLedgerState(TaskLedgerPolicy{Required: true, RequireEvidence: true})
	result := state.apply(workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			{ID: "edit", Content: "Edit README", Status: "completed"},
		},
	})

	if result.Error == "" || !strings.Contains(result.Error, "has no evidence") {
		t.Fatalf("expected missing evidence rejection, got %#v", result)
	}
}

func TestTaskLedgerAcceptsCompletedItemWithWriteReadAndVerifyReceipts(t *testing.T) {
	state := newTaskLedgerState(TaskLedgerPolicy{Required: true, RequireEvidence: true})
	receipts := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read README.md"},
		{Tool: "edit_file", Summary: "Edited README.md"},
		{Tool: "shell", Summary: "Command succeeded", Output: "go test ./..."},
	}
	result := state.apply(workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			{ID: "read", Content: "Inspect README", Status: "completed", Evidence: "read:README.md"},
			{ID: "edit", Content: "Edit README", Status: "completed", Evidence: "write:README.md"},
			{ID: "verify", Content: "Run tests", Status: "completed", Evidence: "verify:go test ./..."},
		},
	}, receipts)

	if result.Error != "" {
		t.Fatalf("expected ledger update, got %#v", result)
	}
}

func TestTaskLedgerRejectsEvidenceNotBackedBySuccessfulReceipts(t *testing.T) {
	state := newTaskLedgerState(TaskLedgerPolicy{Required: true, RequireEvidence: true})
	receipts := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited docs/other.md"},
		{Tool: "shell", Summary: "Command failed", Output: "go test ./...", Error: "exit status 1"},
	}
	result := state.apply(workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			{ID: "edit", Content: "Edit README", Status: "completed", Evidence: "write:README.md"},
		},
	}, receipts)

	if result.Error == "" || !strings.Contains(result.Error, "not backed") {
		t.Fatalf("expected unsupported evidence rejection, got %#v", result)
	}
}

func TestTaskLedgerDoesNotRequireNewEvidenceForAlreadyCompletedItems(t *testing.T) {
	state := newTaskLedgerState(TaskLedgerPolicy{Required: true, RequireEvidence: true})
	initial := state.apply(workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			{ID: "edit", Content: "Edit README", Status: "completed", Evidence: "write:README.md"},
		},
	}, []workspace.ToolResult{{Tool: "edit_file", Summary: "Edited README.md"}})

	if initial.Error != "" {
		t.Fatalf("initial ledger update rejected: %#v", initial)
	}

	repeat := state.apply(workspace.ToolRequest{
		Tool: "task_ledger",
		Todos: []workspace.TaskLedgerItem{
			{ID: "edit", Content: "Edit README", Status: "completed"},
		},
	})
	if repeat.Error != "" {
		t.Fatalf("already-completed ledger item should not require fresh evidence, got %#v", repeat)
	}
}
