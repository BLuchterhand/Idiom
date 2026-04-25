package agent

import (
	"fmt"
	"strings"

	"idiom/internal/workspace"
)

type taskLedgerState struct {
	policy  TaskLedgerPolicy
	created bool
	items   []workspace.TaskLedgerItem
}

func newTaskLedgerState(policy TaskLedgerPolicy) *taskLedgerState {
	return &taskLedgerState{policy: policy}
}

func (s *taskLedgerState) apply(req workspace.ToolRequest) workspace.ToolResult {
	if s == nil || s.policy.Disabled {
		return workspace.ToolResult{
			Tool:    "task_ledger",
			Summary: "Task ledger rejected",
			Error:   "task_ledger is disabled for this runner",
		}
	}
	items := taskLedgerItemsFromRequest(req)
	if len(items) == 0 {
		return workspace.ToolResult{
			Tool:    "task_ledger",
			Summary: "Task ledger rejected",
			Error:   "provide a non-empty todos array with content and status for each item",
		}
	}
	normalized := make([]workspace.TaskLedgerItem, 0, len(items))
	active := 0
	for i, item := range items {
		item.ID = strings.TrimSpace(item.ID)
		item.Content = strings.TrimSpace(item.Content)
		item.Status = normalizeTaskLedgerStatus(item.Status)
		item.Evidence = strings.TrimSpace(item.Evidence)
		if item.ID == "" {
			item.ID = fmt.Sprintf("item-%d", i+1)
		}
		if item.Content == "" {
			return workspace.ToolResult{
				Tool:    "task_ledger",
				Summary: "Task ledger rejected",
				Error:   fmt.Sprintf("todo %s is missing content", item.ID),
			}
		}
		if !validTaskLedgerStatus(item.Status) {
			return workspace.ToolResult{
				Tool:    "task_ledger",
				Summary: "Task ledger rejected",
				Error:   fmt.Sprintf("todo %s has unsupported status %q; use pending, in_progress, completed, blocked, or cancelled", item.ID, item.Status),
			}
		}
		if item.Status == "in_progress" {
			active++
		}
		normalized = append(normalized, item)
	}
	if active > 1 {
		return workspace.ToolResult{
			Tool:    "task_ledger",
			Summary: "Task ledger rejected",
			Error:   "only one task ledger item may be in_progress at a time",
		}
	}

	s.created = true
	s.items = normalized
	return workspace.ToolResult{
		Tool:    "task_ledger",
		Summary: "Task ledger updated: " + s.statusSummary(),
		Output:  s.render(),
	}
}

func (s *taskLedgerState) completionRejection(reply string) (workspace.ToolResult, bool) {
	if s == nil || s.policy.Disabled {
		return workspace.ToolResult{}, false
	}
	if s.policy.Required && !s.created && !taskLedgerFinalHasTerminalReason(reply) {
		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Task ledger completion gate rejected final answer",
			Error:   "this is complex multi-step work and no task_ledger exists. Create a ledger or, if you are not yet acting on a write, answer with an explicit blocker or no-op reason.",
		}, true
	}
	if !s.created {
		return workspace.ToolResult{}, false
	}
	if unfinished := s.unfinishedItems(); len(unfinished) > 0 && !taskLedgerFinalHasTerminalReason(reply) {
		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Task ledger completion gate rejected final answer",
			Error:   "task ledger still has unfinished required work: " + strings.Join(unfinished, "; ") + ". Continue the work, mark the ledger item with a concrete blocker, or if the user asked only for a plan, finish with an explicit planning-exit phrase (e.g. \"plan ready - awaiting approval\").",
		}, true
	}
	if blocked := s.blockedItems(); len(blocked) > 0 && !taskLedgerFinalHasTerminalReason(reply) {
		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Task ledger completion gate rejected final answer",
			Error:   "task ledger contains blocked work: " + strings.Join(blocked, "; ") + ". The final answer must state the concrete blocker.",
		}, true
	}
	return workspace.ToolResult{}, false
}

func (s *taskLedgerState) unfinishedItems() []string {
	if s == nil {
		return nil
	}
	var unfinished []string
	for _, item := range s.items {
		switch item.Status {
		case "pending", "in_progress":
			unfinished = append(unfinished, item.ID+": "+item.Content+" ["+item.Status+"]")
		}
	}
	return unfinished
}

func (s *taskLedgerState) blockedItems() []string {
	if s == nil {
		return nil
	}
	var blocked []string
	for _, item := range s.items {
		if item.Status != "blocked" {
			continue
		}
		text := item.ID + ": " + item.Content
		if item.Evidence != "" {
			text += " (" + item.Evidence + ")"
		}
		blocked = append(blocked, text)
	}
	return blocked
}

func (s *taskLedgerState) statusSummary() string {
	counts := make(map[string]int)
	for _, item := range s.items {
		counts[item.Status]++
	}
	parts := make([]string, 0, 5)
	for _, status := range []string{"completed", "in_progress", "pending", "blocked", "cancelled"} {
		if counts[status] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[status], status))
		}
	}
	if len(parts) == 0 {
		return "empty"
	}
	return strings.Join(parts, ", ")
}

func (s *taskLedgerState) render() string {
	if s == nil || len(s.items) == 0 {
		return ""
	}
	var b strings.Builder
	for _, item := range s.items {
		b.WriteString("- [")
		b.WriteString(item.Status)
		b.WriteString("] ")
		b.WriteString(item.ID)
		b.WriteString(": ")
		b.WriteString(item.Content)
		if item.Evidence != "" {
			b.WriteString(" (evidence: ")
			b.WriteString(item.Evidence)
			b.WriteString(")")
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func taskLedgerItemsFromRequest(req workspace.ToolRequest) []workspace.TaskLedgerItem {
	if len(req.Todos) > 0 {
		return append([]workspace.TaskLedgerItem(nil), req.Todos...)
	}
	return append([]workspace.TaskLedgerItem(nil), req.Items...)
}

func normalizeTaskLedgerStatus(status string) string {
	status = strings.ToLower(strings.TrimSpace(status))
	status = strings.ReplaceAll(status, "-", "_")
	status = strings.ReplaceAll(status, " ", "_")
	return status
}

func validTaskLedgerStatus(status string) bool {
	switch status {
	case "pending", "in_progress", "completed", "blocked", "cancelled":
		return true
	default:
		return false
	}
}

// taskLedgerFinalHasTerminalReason recognizes reply phrasings that let
// the model exit the completion gate without finishing every ledger
// item. Two categories:
//
//  1. Blocker phrases ("blocked", "cannot proceed", "permission") -
//     the work can't move forward for reasons outside the model's
//     control, and the answer should explain the blocker.
//  2. Planning-exit phrases ("plan ready", "awaiting approval") -
//     the user asked for triage only, not execution. Without this
//     escape the gate forces the model to execute every item it
//     just planned, which is the opposite of what the user wanted.
func taskLedgerFinalHasTerminalReason(reply string) bool {
	return containsAny(strings.ToLower(strings.TrimSpace(reply)),
		"blocked",
		"blocker",
		"cannot proceed",
		"could not proceed",
		"unable to",
		"no changes needed",
		"no change needed",
		"already complete",
		"already satisfied",
		"permission",
		"approval",
		"plan ready",
		"plan complete",
		"planning only",
	)
}
