package agent

import (
	"strings"
	"testing"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/workerruntime"
)

func TestSourceEvidenceStateTracksRedirectPendingUntilBodyEvidence(t *testing.T) {
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Redirected internal/surfaces/tui/app/sessions.go to code_navigate",
			Output:  "Use code_navigate for source-body evidence in internal/surfaces/tui/app/sessions.go.",
		},
	}

	evidence := sourceEvidenceForPath(results, "internal/surfaces/tui/app/sessions.go")
	if evidence.Level != sourceEvidenceSourceBodyPending {
		t.Fatalf("Level = %q, want %q", evidence.Level, sourceEvidenceSourceBodyPending)
	}

	if sourceEvidenceBodyReadForPath("internal/surfaces/tui/app/sessions.go", results) {
		t.Fatal("redirect-only evidence must not count as source-body evidence")
	}

	req := workspace.ToolRequest{Tool: "code_navigate", Path: "internal/surfaces/tui/app/sessions.go", Capability: "read_symbol", Pattern: "session"}
	if !sourceEvidencePendingRedirectFollowupAllowed(req, results) {
		t.Fatal("exact redirected source-body follow-up should be allowed")
	}

	for name, badReq := range map[string]workspace.ToolRequest{
		"missing source-body capability": {Tool: "code_navigate", Path: "internal/surfaces/tui/app/sessions.go"},
		"file outline":                   {Tool: "code_navigate", Path: "internal/surfaces/tui/app/sessions.go", Capability: "file_outline"},
		"references":                     {Tool: "code_navigate", Path: "internal/surfaces/tui/app/sessions.go", Capability: "references", Pattern: "session"},
		"read symbol missing pattern":    {Tool: "code_navigate", Path: "internal/surfaces/tui/app/sessions.go", Capability: "read_symbol"},
	} {
		if sourceEvidencePendingRedirectFollowupAllowed(badReq, results) {
			t.Fatalf("%s should not satisfy a redirected source-body obligation", name)
		}
	}

	other := workspace.ToolRequest{Tool: "code_navigate", Path: "internal/surfaces/tui/app/sessions_panel.go", Capability: "read_symbol", Pattern: "session"}
	if sourceEvidencePendingRedirectFollowupAllowed(other, results) {
		t.Fatal("redirect follow-up allowance must stay scoped to the redirected path")
	}

	results = append(results, workspace.ToolResult{
		Tool: "code_navigate",
		Output: "route_summary: Read internal/surfaces/tui/app/sessions.go lines 10-40 (total lines: 120)\n" +
			"func (m sessionModel) moveDown() {\n\tm.cursor++\n}\n",
	})
	evidence = sourceEvidenceForPath(results, "internal/surfaces/tui/app/sessions.go")

	if !sourceEvidenceHasBody(evidence) {
		t.Fatalf("Level = %q, want source-body evidence after routed body read", evidence.Level)
	}

	if pending := sourceEvidencePendingRedirects(results); len(pending) != 0 {
		t.Fatalf("pending redirects after body evidence = %#v, want none", pending)
	}
}

func TestSourceEvidenceIgnoresGeneratedArtifacts(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 .idiom/comprehension/views/context.json lines 1-20 (total lines: 20)",
		Output:  "1:{\"candidate_files\":[{\"path\":\"internal/runtime/agent/runner.go\"}]}\n",
	}}
	if sourceEvidenceBodyReadForPath(".idiom/comprehension/views/context.json", results) {
		t.Fatal("generated context artifacts must not satisfy source-body evidence")
	}

	if paths := sourceEvidenceBodyPaths(results); len(paths) != 0 {
		t.Fatalf("generated context artifacts must not appear in source-body path list: %#v", paths)
	}
}

func TestSourceEvidenceIgnoresCandidateAnchorMiss(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:    "go_read_symbol",
		Summary: `Go symbol read path-filter miss; found 1 candidate anchor(s) for "Model.Update"`,
		Output: strings.Join([]string{
			"requested_symbol: Model.Update",
			"requested_path: internal/surfaces/tui/app/model_state.go",
			"path_filter_status: no source-body match in requested path",
			"candidate_anchor_status: anchors only; read one candidate path to inspect source body",
			"candidate_anchors:",
			"- internal/surfaces/tui/app/lifecycle.go:409 method Model.Update",
		}, "\n"),
	}}

	if sourceEvidenceBodyReadForPath("internal/surfaces/tui/app/lifecycle.go", results) {
		t.Fatal("candidate anchor must not count as source-body evidence for the candidate path")
	}

	if paths := sourceEvidenceBodyPaths(results); len(paths) != 0 {
		t.Fatalf("candidate anchor produced source-body paths: %#v", paths)
	}
}

func TestSourceEvidencePendingRedirectProgressAllowsOneOutlineBeforeBody(t *testing.T) {
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
			Output:  "func renderSessionsPanelContent() {}\nfunc handleSessionsPanelKey() {}\n",
		},
	}

	outline := workspace.ToolRequest{
		Tool:            "code_navigate",
		Path:            "internal/surfaces/tui/app/sessions.go",
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
			"route_summary: Outlined 1 top-level declaration(s) in internal/surfaces/tui/app/sessions.go.",
			"declarations:",
			"- sessionModel lines 8-24",
		}, "\n"),
	})
	if sourceEvidencePendingRedirectProgressAllowedForBrief(brief, outline, withOutline) {
		t.Fatal("same-path outline progress should be single-use while the body obligation remains pending")
	}

	body := workspace.ToolRequest{Tool: "code_navigate", Path: "internal/surfaces/tui/app/sessions.go", Capability: "read_symbol", Pattern: "sessionModel"}
	if !sourceEvidencePendingRedirectProgressAllowedForBrief(brief, body, withOutline) {
		t.Fatal("source-body follow-up should remain allowed after outline progress")
	}

	if sourceEvidenceBodyReadForPath("internal/surfaces/tui/app/sessions.go", withOutline) {
		t.Fatal("outline progress must not count as source-body evidence")
	}
}

func TestSourceEvidenceStateTracksDirectOutlineRedirectToRelatedReadSymbol(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: "implementation",
		Contract:   "implementation_result",
		Objective:  "fix workspace tool schemas",
		TargetPacket: []string{
			"candidate file: internal/capabilities/workspace/tool_schemas.go",
		},
		AllowedTools: []string{"go_file_outline", "go_read_symbol", "edit_file"},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Redirected internal/capabilities/workspace/tool_schemas.go to go_file_outline",
			Output: strings.Join([]string{
				`candidate_request: {"tool":"go_file_outline","path":"internal/capabilities/workspace/tool_schemas.go"}`,
				"related_capability: read_symbol via go_read_symbol returns one declaration body by name.",
			}, "\n"),
		},
	}

	pending := sourceEvidencePendingRedirectsForBrief(brief, results)
	if len(pending) != 1 {
		t.Fatalf("pending redirects = %#v, want one related symbol-body obligation", pending)
	}

	if pending[0].PendingTool != "go_read_symbol" {
		t.Fatalf("PendingTool = %q, want go_read_symbol", pending[0].PendingTool)
	}

	tools := sourceEvidencePendingRedirectTools(brief.AllowedTools, pending)
	for _, want := range []string{"go_read_symbol", "go_file_outline"} {
		if !containsSourceEvidenceTool(tools, want) {
			t.Fatalf("pending redirect tools missing %q: %#v", want, tools)
		}
	}

	outline := workspace.ToolRequest{
		Tool:            "go_file_outline",
		Path:            "internal/capabilities/workspace/tool_schemas.go",
		NamedGap:        "need declarations before selecting a symbol body",
		WhyNotAnswering: "the raw read redirected to an outline route first",
	}
	if !sourceEvidencePendingRedirectProgressAllowedForBrief(brief, outline, results) {
		t.Fatal("direct outline redirect should allow one same-path outline before symbol body")
	}

	body := workspace.ToolRequest{
		Tool:   "go_read_symbol",
		Path:   "internal/capabilities/workspace/tool_schemas.go",
		Symbol: "ToolSchemas",
	}
	if !sourceEvidencePendingRedirectFollowupAllowedForBrief(brief, body, results) {
		t.Fatal("related read_symbol route should satisfy the redirected source-body obligation")
	}
}

func TestSourceEvidencePendingRedirectsForBriefDropsStaleRedirectAfterPlausibleBodyEvidence(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: "implementation",
		Contract:   "implementation_result",
		Objective:  "fix the session pane cursor scrolling",
		TargetPacket: []string{
			"candidate file: internal/surfaces/tui/app/implementation_dispatch.go",
			"candidate file: internal/surfaces/tui/app/sessions_panel.go",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Redirected internal/surfaces/tui/app/implementation_dispatch.go to code_navigate",
			Output:  "Use code_navigate for source-body evidence in internal/surfaces/tui/app/implementation_dispatch.go.",
		},
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/surfaces/tui/app/sessions_panel.go lines 1-219 (total lines: 219)",
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

func containsSourceEvidenceTool(tools []string, want string) bool {
	for _, tool := range tools {
		if tool == want {
			return true
		}
	}

	return false
}

func TestSourceEvidencePendingRedirectsForBriefKeepsPlausibleRedirect(t *testing.T) {
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
			Output:  "func renderSessionsPanelContent() {}\nfunc handleSessionsPanelKey() {}\n",
		},
	}

	pending := sourceEvidencePendingRedirectsForBrief(brief, results)
	if len(pending) != 1 || pending[0].Path != "internal/surfaces/tui/app/sessions.go" {
		t.Fatalf("scoped pending redirects = %#v, want plausible redirect retained", pending)
	}
}
