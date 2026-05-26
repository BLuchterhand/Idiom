package agent

import (
	"strings"
	"testing"

	"idiom/internal/frontier/workerruntime"
	"idiom/internal/workspace"
)

func TestSourceEvidenceStateTracksRedirectPendingUntilBodyEvidence(t *testing.T) {
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Redirected internal/app/sessions.go to code_navigate",
			Output:  "Use code_navigate for source-body evidence in internal/app/sessions.go.",
		},
	}

	evidence := sourceEvidenceForPath(results, "internal/app/sessions.go")
	if evidence.Level != sourceEvidenceSourceBodyPending {
		t.Fatalf("Level = %q, want %q", evidence.Level, sourceEvidenceSourceBodyPending)
	}
	if sourceEvidenceBodyReadForPath("internal/app/sessions.go", results) {
		t.Fatal("redirect-only evidence must not count as source-body evidence")
	}
	req := workspace.ToolRequest{Tool: "code_navigate", Path: "internal/app/sessions.go", Capability: "read_symbol", Pattern: "session"}
	if !sourceEvidencePendingRedirectFollowupAllowed(req, results) {
		t.Fatal("exact redirected source-body follow-up should be allowed")
	}
	for name, badReq := range map[string]workspace.ToolRequest{
		"missing source-body capability": {Tool: "code_navigate", Path: "internal/app/sessions.go"},
		"file outline":                   {Tool: "code_navigate", Path: "internal/app/sessions.go", Capability: "file_outline"},
		"references":                     {Tool: "code_navigate", Path: "internal/app/sessions.go", Capability: "references", Pattern: "session"},
		"read symbol missing pattern":    {Tool: "code_navigate", Path: "internal/app/sessions.go", Capability: "read_symbol"},
	} {
		if sourceEvidencePendingRedirectFollowupAllowed(badReq, results) {
			t.Fatalf("%s should not satisfy a redirected source-body obligation", name)
		}
	}
	other := workspace.ToolRequest{Tool: "code_navigate", Path: "internal/app/sessions_panel.go", Capability: "read_symbol", Pattern: "session"}
	if sourceEvidencePendingRedirectFollowupAllowed(other, results) {
		t.Fatal("redirect follow-up allowance must stay scoped to the redirected path")
	}

	results = append(results, workspace.ToolResult{
		Tool: "code_navigate",
		Output: "route_summary: Read internal/app/sessions.go lines 10-40 (total lines: 120)\n" +
			"func (m sessionModel) moveDown() {\n\tm.cursor++\n}\n",
	})
	evidence = sourceEvidenceForPath(results, "internal/app/sessions.go")
	if !sourceEvidenceHasBody(evidence) {
		t.Fatalf("Level = %q, want source-body evidence after routed body read", evidence.Level)
	}
	if pending := sourceEvidencePendingRedirects(results); len(pending) != 0 {
		t.Fatalf("pending redirects after body evidence = %#v, want none", pending)
	}
}

func TestSourceEvidencePendingRedirectProgressAllowsOneOutlineBeforeBody(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: "implementation",
		Contract:   "implementation_result",
		Objective:  "fix the session pane cursor scrolling",
		TargetPacket: []string{
			"candidate file: internal/app/sessions.go",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Redirected internal/app/sessions.go to code_navigate",
			Output:  "Use code_navigate for source-body evidence in internal/app/sessions.go.",
		},
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/app/sessions_panel.go lines 1-219 (total lines: 219)",
			Output:  "func renderSessionsPanelContent() {}\nfunc handleSessionsPanelKey() {}\n",
		},
	}

	outline := workspace.ToolRequest{
		Tool:            "code_navigate",
		Path:            "internal/app/sessions.go",
		Capability:      "file_outline",
		NamedGap:        "declarations needed before selecting source-body evidence",
		WhyNotAnswering: "need symbol names for the redirected source-body follow-up",
	}
	if sourceEvidencePendingRedirectFollowupAllowedForBrief(brief, outline, results) {
		t.Fatal("outline progress must not satisfy the source-body follow-up contract")
	}
	if !sourceEvidencePendingRedirectProgressAllowedForBrief(brief, outline, results) {
		t.Fatal("the exact redirected same-path outline should be allowed as progress before a symbol is known")
	}
	withoutGap := outline
	withoutGap.NamedGap = ""
	withoutGap.WhyNotAnswering = ""
	if sourceEvidencePendingRedirectProgressAllowedForBrief(brief, withoutGap, results) {
		t.Fatal("outline progress should require a named gap or why_not_answering reason")
	}
	withOutline := append(append([]workspace.ToolResult{}, results...), workspace.ToolResult{
		Tool:    "code_navigate",
		Summary: "Code navigation routed file_outline via go_file_outline",
		Output: strings.Join([]string{
			"capability: file_outline",
			"language: go",
			"routed_tool: go_file_outline",
			"route_summary: Outlined 1 top-level declaration(s) in internal/app/sessions.go.",
			"declarations:",
			"- sessionModel lines 8-24",
		}, "\n"),
	})
	if sourceEvidencePendingRedirectProgressAllowedForBrief(brief, outline, withOutline) {
		t.Fatal("same-path outline progress should be single-use while the body obligation remains pending")
	}
	body := workspace.ToolRequest{Tool: "code_navigate", Path: "internal/app/sessions.go", Capability: "read_symbol", Pattern: "sessionModel"}
	if !sourceEvidencePendingRedirectProgressAllowedForBrief(brief, body, withOutline) {
		t.Fatal("source-body follow-up should remain allowed after outline progress")
	}
	if sourceEvidenceBodyReadForPath("internal/app/sessions.go", withOutline) {
		t.Fatal("outline progress must not count as source-body evidence")
	}
}

func TestSourceEvidencePendingRedirectsForBriefDropsStaleRedirectAfterPlausibleBodyEvidence(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: "implementation",
		Contract:   "implementation_result",
		Objective:  "fix the session pane cursor scrolling",
		TargetPacket: []string{
			"candidate file: internal/app/implementation_dispatch.go",
			"candidate file: internal/app/sessions_panel.go",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Redirected internal/app/implementation_dispatch.go to code_navigate",
			Output:  "Use code_navigate for source-body evidence in internal/app/implementation_dispatch.go.",
		},
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/app/sessions_panel.go lines 1-219 (total lines: 219)",
			Output:  "func renderSessionsPanelContent() {}\nfunc handleSessionsPanelKey() {}\n",
		},
	}

	if pending := sourceEvidencePendingRedirects(results); len(pending) != 1 {
		t.Fatalf("raw pending redirects = %#v, want the initial redirect retained for diagnostics", pending)
	}
	if pending := sourceEvidencePendingRedirectsForBrief(brief, results); len(pending) != 0 {
		t.Fatalf("scoped pending redirects = %#v, want stale unrelated redirect dropped after plausible source-body evidence", pending)
	}
}

func TestSourceEvidencePendingRedirectsForBriefKeepsPlausibleRedirect(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: "implementation",
		Contract:   "implementation_result",
		Objective:  "fix the session pane cursor scrolling",
		TargetPacket: []string{
			"candidate file: internal/app/sessions.go",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Redirected internal/app/sessions.go to code_navigate",
			Output:  "Use code_navigate for source-body evidence in internal/app/sessions.go.",
		},
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/app/sessions_panel.go lines 1-219 (total lines: 219)",
			Output:  "func renderSessionsPanelContent() {}\nfunc handleSessionsPanelKey() {}\n",
		},
	}

	pending := sourceEvidencePendingRedirectsForBrief(brief, results)
	if len(pending) != 1 || pending[0].Path != "internal/app/sessions.go" {
		t.Fatalf("scoped pending redirects = %#v, want plausible redirect retained", pending)
	}
}
