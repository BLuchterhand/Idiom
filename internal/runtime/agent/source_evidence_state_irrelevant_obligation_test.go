package agent

import (
	"testing"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/workerruntime"
)

func TestSourceEvidencePendingRedirectsForBriefDropsIrrelevantWithoutScopeHints(t *testing.T) {
	// When scope hints (TargetPacket, TargetRefs, EvidencePaths, UserBoundaries)
	// are all empty, the relevance filter should still run using the objective
	// text. An obligation on a path unrelated to the objective should be dropped.
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: "implementation",
		Contract:   "implementation_result",
		Objective:  "fix the /clear slash command in the sidebar",
		// Deliberately empty: no TargetPacket, TargetRefs, EvidencePaths, or UserBoundaries.
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Redirected internal/surfaces/tui/app/history_audit.go to code_navigate",
			Output:  "Use code_navigate for source-body evidence in internal/surfaces/tui/app/history_audit.go.",
		},
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/surfaces/tui/app/slash_commands.go lines 1-377 (total lines: 377)",
			Output:  "func slashClear() { m.messages = nil }\n",
		},
	}

	pending := sourceEvidencePendingRedirectsForBrief(brief, results)
	// history_audit.go shares no terms with "fix the /clear slash command in the sidebar"
	// and is not in any scope hint list, so it should be filtered out.
	if len(pending) != 0 {
		t.Fatalf("scoped pending redirects = %#v, want empty: irrelevant path without scope hints should be dropped", pending)
	}
}

func TestSourceEvidencePendingRedirectsForBriefDropsExhaustedObligation(t *testing.T) {
	// After 3+ failed code_navigate attempts on the same path, the obligation
	// should be dropped even if the path is otherwise relevant.
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: "implementation",
		Contract:   "implementation_result",
		Objective:  "fix the session pane cursor scrolling",
		TargetPacket: []string{
			"candidate file: internal/surfaces/tui/app/sessions.go",
			"candidate file: internal/surfaces/tui/app/sessions_panel.go",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Redirected internal/surfaces/tui/app/sessions.go to code_navigate",
			Output:  "Use code_navigate for source-body evidence in internal/surfaces/tui/app/sessions.go.",
		},
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/surfaces/tui/app/sessions_panel.go lines 1-219 (total lines: 219)",
			Output:  "func renderSessionsPanelContent() {}\n",
		},
		// Three failed code_navigate attempts on the obligation path.
		{
			Tool:    "code_navigate",
			Summary: "Code navigation needs route context",
			Error:   "No generic code navigation matches for find_symbol",
			Output:  "internal/surfaces/tui/app/sessions.go",
		},
		{
			Tool:    "code_navigate",
			Summary: "No generic code navigation matches for find_symbol",
			Error:   "replayed same blocker",
			Output:  "internal/surfaces/tui/app/sessions.go",
		},
		{
			Tool:    "code_navigate",
			Summary: "Repeated dead-end action rejected",
			Error:   "zero-hit retry blocked",
			Output:  "internal/surfaces/tui/app/sessions.go",
		},
	}

	pending := sourceEvidencePendingRedirectsForBrief(brief, results)
	if len(pending) != 0 {
		t.Fatalf("scoped pending redirects = %#v, want empty: exhausted obligation should be dropped after %d failed code_navigate attempts", pending, maxObligationResolutionAttempts)
	}
}

func TestSourceEvidencePendingRedirectsForBriefKeepsNonExhaustedObligation(t *testing.T) {
	// With fewer than maxObligationResolutionAttempts failures, the obligation
	// should be kept if the path is relevant.
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: "implementation",
		Contract:   "implementation_result",
		Objective:  "fix the session pane cursor scrolling",
		TargetPacket: []string{
			"candidate file: internal/surfaces/tui/app/sessions.go",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Redirected internal/surfaces/tui/app/sessions.go to code_navigate",
			Output:  "Use code_navigate for source-body evidence in internal/surfaces/tui/app/sessions.go.",
		},
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/surfaces/tui/app/sessions_panel.go lines 1-219 (total lines: 219)",
			Output:  "func renderSessionsPanelContent() {}\n",
		},
		// Only two failed code_navigate attempts — below the threshold.
		{
			Tool:    "code_navigate",
			Summary: "Code navigation needs route context",
			Error:   "No generic code navigation matches",
			Output:  "internal/surfaces/tui/app/sessions.go",
		},
		{
			Tool:    "code_navigate",
			Summary: "No generic code navigation matches for find_symbol",
			Error:   "replayed same blocker",
			Output:  "internal/surfaces/tui/app/sessions.go",
		},
	}

	pending := sourceEvidencePendingRedirectsForBrief(brief, results)
	if len(pending) != 1 || pending[0].Path != "internal/surfaces/tui/app/sessions.go" {
		t.Fatalf("scoped pending redirects = %#v, want sessions.go obligation retained: not yet exhausted (%d < %d)", pending, 2, maxObligationResolutionAttempts)
	}
}

func TestObligationResolutionAttemptsCountsOnlyNavigationFailures(t *testing.T) {
	results := []workspace.ToolResult{
		// Successful code_navigate — not counted.
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "internal/surfaces/tui/app/sessions.go",
		},
		// Failed code_navigate — counted.
		{
			Tool:    "code_navigate",
			Summary: "No generic code navigation matches",
			Error:   "no symbolic match",
			Output:  "internal/surfaces/tui/app/sessions.go",
		},
		// read_file on the path — not code_navigate, not counted.
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/surfaces/tui/app/sessions.go",
			Output:  "internal/surfaces/tui/app/sessions.go",
		},
		// code_navigate on a different path — not counted.
		{
			Tool:    "code_navigate",
			Summary: "No generic code navigation matches",
			Error:   "no matches",
			Output:  "internal/surfaces/tui/app/other.go",
		},
	}
	attempts := sourceEvidenceObligationResolutionAttempts("internal/surfaces/tui/app/sessions.go", results)
	if attempts != 1 {
		t.Fatalf("resolution attempts = %d, want 1 (only the one failed code_navigate on the path should count)", attempts)
	}
}
