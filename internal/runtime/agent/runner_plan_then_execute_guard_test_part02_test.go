package agent

import (
	"slices"
	"strings"
	"testing"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

func TestPlanThenExecuteRequiredPathDiscoveryRejectsBroadReadBeforeRequiredSource(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/workflows/productworkflow/coverage_gate.go"},
	}
	summary, errorText, reject := planThenExecuteRequiredPathDiscoveryRejection(workspace.ToolRequest{
		Tool:            "context_query",
		Query:           "what calls productWorkflow",
		Resolution:      "symbol",
		IncludeEvidence: true,
	}, brief, nil)

	if !reject {
		t.Fatalf("broad/semantic discovery should be rejected until required source is read")
	}

	if summary != "Required edit path source read required" {
		t.Fatalf("unexpected summary: %q", summary)
	}

	if !strings.Contains(errorText, "cmd/idiom/product_workflow.go") {
		t.Fatalf("rejection should point at first unread required path, got %q", errorText)
	}
}

func TestPlanThenExecuteRequiredPathDiscoveryAllowsUnreadRequiredPathRead(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/workflows/productworkflow/coverage_gate.go"},
	}
	_, _, reject := planThenExecuteRequiredPathDiscoveryRejection(workspace.ToolRequest{
		Tool: "read_file",
		Path: "cmd/idiom/product_workflow.go",
	}, brief, nil)

	if reject {
		t.Fatalf("read_file on an unread required path must be allowed")
	}
}

func TestPlanThenExecuteRequiredPathDiscoveryRejectsAlreadyReadPathUntilRemainingRead(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/workflows/productworkflow/coverage_gate.go"},
	}
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n\n1515:func runProductTDD() {}\n",
	}}
	_, errorText, reject := planThenExecuteRequiredPathDiscoveryRejection(workspace.ToolRequest{
		Tool:       "code_navigate",
		Path:       "cmd/idiom/product_workflow.go",
		Capability: "read_symbol",
		Pattern:    "runProductTDD",
	}, brief, results)

	if !reject {
		t.Fatalf("repeat discovery on an already-read required path should be rejected while another required path is unread")
	}

	if !strings.Contains(errorText, "internal/workflows/productworkflow/coverage_gate.go") {
		t.Fatalf("rejection should point at remaining unread required path, got %q", errorText)
	}
}

func TestPlanThenExecuteRequiredPathDiscoveryNamesSourceBodyRouteAfterRedirect(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go"},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n\n1515:func runProductTDD() {}\n",
		},
		{
			Tool:    "read_file",
			Summary: "Redirected cmd/idiom/product_workflow_test.go to code_navigate",
			Output:  `candidate_request: {"tool":"code_navigate","capability":"file_outline","path":"cmd/idiom/product_workflow_test.go","language":"go"}`,
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed file_outline via go_file_outline",
			Output:  "route_summary: Outlined 33 top-level declaration(s) in cmd/idiom/product_workflow_test.go.\n",
		},
	}

	_, errorText, reject := planThenExecuteRequiredPathDiscoveryRejection(workspace.ToolRequest{
		Tool:       "code_navigate",
		Path:       "cmd/idiom/product_workflow.go",
		Capability: "read_symbol",
		Pattern:    "runProductTDD",
	}, brief, results)
	if !reject {
		t.Fatalf("repeat discovery on an already-read required path should be rejected while redirected path lacks source-body evidence")
	}

	for _, want := range []string{
		"cmd/idiom/product_workflow_test.go",
		"code_navigate",
		"read_symbol",
		"start_line",
		"Do not repeat whole-file `read_file`",
	} {
		if !strings.Contains(errorText, want) {
			t.Fatalf("rejection missing %q:\n%s", want, errorText)
		}
	}
}

func TestPlanThenExecuteRequiredPathDiscoveryDoesNotCountFileOutlineAsSourceRead(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/workflows/productworkflow/coverage_gate.go"},
	}
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed file_outline via go_file_outline",
		Output:  "route_summary: Outlined 65 top-level declaration(s) in cmd/idiom/product_workflow.go. Includes declaration tags and suggested move batches in the output.\n",
	}}
	_, errorText, reject := planThenExecuteRequiredPathDiscoveryRejection(workspace.ToolRequest{
		Tool:       "code_navigate",
		Path:       "cmd/idiom/product_workflow.go",
		Capability: "read_symbol",
		Pattern:    "runProductTDD",
	}, brief, results)

	if reject {
		t.Fatalf("file_outline should orient but not satisfy source-read coverage; follow-up source read should remain allowed: %s", errorText)
	}
}

func TestPlanThenExecuteRequiredPathDiscoveryAllowsBoundedInvalidAnchorRefreshBeforeUnreadPath(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/workflows/productworkflow/coverage_gate.go"},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n\n1515:func runProductTDD() {}\n",
		},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "invalid edit anchor(s): `cmd/idiom/product_workflow.go: old_text not found in current source`; missing required edit path(s): `internal/workflows/productworkflow/coverage_gate.go`.",
		},
	}

	_, _, reject := planThenExecuteRequiredPathDiscoveryRejection(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "cmd/idiom/product_workflow.go",
		StartLine: 1515,
		EndLine:   1640,
	}, brief, results)
	if reject {
		t.Fatalf("bounded source-body refresh for a rejected invalid anchor should be allowed before the remaining path is read")
	}
}

func TestPlanThenExecuteRejectedPlanSourceRefreshAllowedForInvalidAnchor(t *testing.T) {
	required := []string{"cmd/idiom/product_workflow.go", "internal/workflows/productworkflow/coverage_gate.go"}
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n\n1515:func runProductTDD() {}\n",
		},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "invalid edit anchor(s): `cmd/idiom/product_workflow.go: old_text not found in current source`; missing required edit path(s): `internal/workflows/productworkflow/coverage_gate.go`.",
		},
	}

	if !planThenExecuteRejectedPlanSourceRefreshAllowed(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "cmd/idiom/product_workflow.go",
		StartLine: 1515,
		EndLine:   1640,
	}, required, results) {

		t.Fatal("bounded source-body refresh for invalid submit_plan anchor should be allowed")
	}

	if planThenExecuteRejectedPlanSourceRefreshAllowed(workspace.ToolRequest{
		Tool: "read_file",
		Path: "cmd/idiom/product_workflow.go",
	}, required, results) {

		t.Fatal("unbounded file refresh should not bypass submit_plan-only recovery")
	}

	if planThenExecuteRejectedPlanSourceRefreshAllowed(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "internal/workflows/productworkflow/other.go",
		StartLine: 1,
		EndLine:   20,
	}, required, results) {

		t.Fatal("refresh should stay inside rejected required paths")
	}
}

func TestPlanThenExecuteRejectedPlanSourceRefreshAllowedForPreflightSyntaxValidation(t *testing.T) {
	required := []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go"}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read f1 cmd/idiom/product_workflow.go lines 80-125",
			Output:  "80:type productValidateArtifactOutput struct {}\n100:func runProductValidateArtifact() {}\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read f2 cmd/idiom/product_workflow_test.go lines 1-80",
			Output:  "1:package main\n",
		},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "edits[0]: preflight syntax validation failed for cmd/idiom/product_workflow.go at line 100: ./cmd/idiom/product_workflow.go:100:6: expected '(', found runProductValidateArtifact",
		},
	}

	if !planThenExecuteRejectedPlanSourceRefreshAllowed(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "cmd/idiom/product_workflow.go",
		StartLine: 80,
		EndLine:   125,
	}, required, results) {

		t.Fatal("bounded source-body refresh for submit_plan syntax preflight failure should be allowed")
	}

	if planThenExecuteRejectedPlanSourceRefreshAllowed(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "cmd/idiom/product_workflow_test.go",
		StartLine: 1,
		EndLine:   80,
	}, required, results) {

		t.Fatal("syntax preflight refresh should stay on the path named by the failed plan")
	}

	note := planThenExecutePostSubmitRejectionRefreshNote(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: required,
	}, results, results[len(results)-1], len(results)-1)
	if !strings.Contains(note, "cmd/idiom/product_workflow.go") || strings.Contains(note, "No further read") {
		t.Fatalf("refresh note should name syntax-failed path, got:\n%s", note)
	}
}

func TestPlanThenExecuteCandidateRejectedPlanRefreshRedirectsToSyntaxLine(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "context_pack",
			Summary: "Context pack wrote candidates",
			Output:  `{"candidate_files":[{"path":"internal/workflows/productworkflow/coverage_gate.go"}]}`,
		},
		{
			Tool:    "read_file",
			Summary: "Read f1 internal/workflows/productworkflow/coverage_gate.go lines 1-391 (total lines: 391)",
			Output:  "1:package productworkflow\n",
		},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "edits[1]: preflight syntax validation failed for internal/workflows/productworkflow/coverage_gate.go at line 125: ./internal/workflows/productworkflow/coverage_gate.go:125:6: expected '(', found appendAuditBlockers",
		},
	}
	req := workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "internal/workflows/productworkflow/coverage_gate.go",
		StartLine: 1,
		EndLine:   50,
	}

	redirected, _, ok := runner.planThenExecuteRejectedPlanRefreshReadRedirect(req, results)
	if !ok {
		t.Fatal("expected rejected-plan refresh read to redirect to syntax failure region")
	}

	if redirected.Path != "internal/workflows/productworkflow/coverage_gate.go" || redirected.StartLine > 125 || redirected.EndLine < 125 {
		t.Fatalf("redirected read should cover syntax failure line 125, got %#v", redirected)
	}

	if !runner.planThenExecuteCandidateSourceRefreshAllowed(redirected, results, 0) {
		t.Fatalf("redirected candidate refresh should be allowed after rejected submit_plan")
	}
}

func TestPlanThenExecuteRequiredPathDiscoveryRejectsUnboundedInvalidAnchorRefresh(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/workflows/productworkflow/coverage_gate.go"},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n\n1515:func runProductTDD() {}\n",
		},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "invalid edit anchor(s): `cmd/idiom/product_workflow.go: old_text not found in current source`; missing required edit path(s): `internal/workflows/productworkflow/coverage_gate.go`.",
		},
	}

	summary, errorText, reject := planThenExecuteRequiredPathDiscoveryRejection(workspace.ToolRequest{
		Tool: "read_file",
		Path: "cmd/idiom/product_workflow.go",
	}, brief, results)
	if !reject {
		t.Fatalf("unbounded invalid-anchor refresh should be rejected because it can redirect without source-body evidence")
	}

	if summary != "Bounded submit_plan anchor refresh required" {
		t.Fatalf("unexpected summary: %q", summary)
	}

	for _, want := range []string{"deterministic fallback `read_file`", "start_line:1", "end_line:120", "read_symbol", "internal/workflows/productworkflow/coverage_gate.go"} {
		if !strings.Contains(errorText, want) {
			t.Fatalf("rejection missing %q:\n%s", want, errorText)
		}
	}
}

func TestPlanThenExecuteAnchorRefreshFallbackInstructionNamesExactPath(t *testing.T) {
	got := planThenExecuteAnchorRefreshReadFallbackInstruction([]string{"./native/src/simulation/world/collapse_event.h"})

	for _, want := range []string{
		"native/src/simulation/world/collapse_event.h",
		"deterministic fallback `read_file`",
		"start_line:1",
		"end_line:120",
		"read_symbol",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("fallback instruction missing %q:\n%s", want, got)
		}
	}
}

func TestPlanThenExecutePostReadinessDiscoveryRejectsMoreSearch(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/workflows/productworkflow/coverage_gate.go"},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n\n1515:func runProductTDD() {}\n",
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read EvaluateCoverageGate in internal/workflows/productworkflow/coverage_gate.go lines 1-120\n\n1:func EvaluateCoverageGate() {}\n",
		},
	}
	summary, errorText, reject := planThenExecutePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:    "search_content",
		Path:    "cmd/idiom/product_workflow.go",
		Pattern: "coverage gate",
	}, brief, results)

	if !reject {
		t.Fatal("post-readiness read/search should be rejected")
	}

	if summary != "Plan submission required" {
		t.Fatalf("unexpected summary: %q", summary)
	}

	if !strings.Contains(errorText, "Submit one parser-readable `submit_plan`") {
		t.Fatalf("rejection should force plan submission, got %q", errorText)
	}
}

func TestPlanThenExecutePostReadinessDiscoveryRejectsAfterBadSubmitPlan(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/workflows/productworkflow/coverage_gate.go"},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n\n1515:func runProductTDD() {}\n",
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read EvaluateCoverageGate in internal/workflows/productworkflow/coverage_gate.go lines 1-120\n\n1:func EvaluateCoverageGate() {}\n",
		},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "submit_plan must use edits[] for changed required paths and top-level source_satisfied_paths for already-final required paths; missing required edit path(s): `internal/workflows/productworkflow/coverage_gate.go`; invalid edit anchor(s): `cmd/idiom/product_workflow.go: old_text not found in current source`.",
		},
	}
	summary, errorText, reject := planThenExecutePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:    "search_content",
		Path:    "cmd/idiom/product_workflow.go",
		Pattern: "runProductTDD",
	}, brief, results)

	if !reject {
		t.Fatal("post-submit-plan recovery search should be rejected")
	}

	if summary != "Bounded submit_plan anchor refresh required" {
		t.Fatalf("unexpected summary: %q", summary)
	}

	if !strings.Contains(errorText, "previous `submit_plan` was rejected before it became a valid plan artifact") || !strings.Contains(errorText, "cmd/idiom/product_workflow.go") {
		t.Fatalf("rejection should point to the remaining stale anchor refresh, got %q", errorText)
	}

	for _, want := range []string{"deterministic fallback `read_file`", "start_line:1", "end_line:120"} {
		if !strings.Contains(errorText, want) {
			t.Fatalf("rejection should include deterministic refresh fallback %q, got %q", want, errorText)
		}
	}

	if strings.Contains(errorText, "corrected parser-readable `submit_plan`") {
		t.Fatalf("rejection must not require submit_plan while an anchor refresh remains, got %q", errorText)
	}

	if !strings.Contains(errorText, "Last submit_plan rejection:") || !strings.Contains(errorText, "missing required edit path") {
		t.Fatalf("rejection should preserve exact submit_plan failure details, got %q", errorText)
	}

	rejected, rejectedAt, ok := lastRejectedSubmitPlanCoverageResult(results)
	if !ok {
		t.Fatal("fixture should include a rejected submit_plan")
	}

	refreshPaths := planThenExecuteRejectedPlanRefreshPaths(brief, results, rejected, rejectedAt)
	if len(refreshPaths) != 1 || refreshPaths[0] != "cmd/idiom/product_workflow.go" {
		t.Fatalf("refreshPaths = %#v, want only remaining invalid-anchor path", refreshPaths)
	}

	_, _, reject = planThenExecutePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool: "read_file",
		Path: "cmd/idiom/product_workflow.go",
	}, brief, results)
	if !reject {
		t.Fatal("unbounded read_file on the stale required-path anchor should be rejected")
	}

	_, _, reject = planThenExecutePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "cmd/idiom/product_workflow.go",
		StartLine: 1515,
		EndLine:   1640,
	}, brief, results)
	if reject {
		t.Fatal("one bounded read_file on the stale required-path anchor should be allowed")
	}

	_, _, reject = planThenExecutePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:       "code_navigate",
		Path:       "cmd/idiom/product_workflow.go",
		Capability: "read_symbol",
		Pattern:    "runProductTDD",
	}, brief, results)
	if reject {
		t.Fatal("one source-body code_navigate read on the stale required-path anchor should be allowed")
	}

	_, _, reject = planThenExecutePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool: "read_file",
		Path: "internal/workflows/productworkflow/coverage_gate.go",
	}, brief, results)
	if !reject {
		t.Fatal("unbounded read_file on a missing required plan path should be rejected")
	}

	_, _, reject = planThenExecutePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "internal/workflows/productworkflow/coverage_gate.go",
		StartLine: 1,
		EndLine:   120,
	}, brief, results)
	if !reject {
		t.Fatal("bounded read_file on a missing required plan path should be rejected when prior source-body evidence already exists")
	}

	outlineOnlyMissingPathResults := []workspace.ToolResult{
		results[0],
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed file_outline via go_file_outline",
			Output:  "route_summary: Outlined 3 top-level declaration(s) in internal/workflows/productworkflow/coverage_gate.go.\n",
		},
		results[2],
	}

	_, _, reject = planThenExecutePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "internal/workflows/productworkflow/coverage_gate.go",
		StartLine: 1,
		EndLine:   120,
	}, brief, outlineOnlyMissingPathResults)

	if !reject {
		t.Fatal("bounded read_file on a missing required coverage path with only outline evidence should be rejected; correction must use submit_plan")
	}

	withRefresh := append(append([]workspace.ToolResult(nil), results...), workspace.ToolResult{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n1515:func runProductTDD() {}",
	})

	_, errorText, reject = planThenExecutePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "cmd/idiom/product_workflow.go",
		StartLine: 1515,
		EndLine:   1640,
	}, brief, withRefresh)

	if !reject {
		t.Fatal("second bounded source-body read on same stale anchor should be rejected")
	}

	if !strings.Contains(errorText, "No further read/search/discovery action is currently allowed") {
		t.Fatalf("second anchor refresh should clearly require corrected submit_plan, got %q", errorText)
	}

	for _, forbidden := range []string{
		"report a path-specific blocker",
		"answer `<<QUESTION>>:`",
		"Only use `<<QUESTION>>:`",
	} {
		if strings.Contains(errorText, forbidden) {
			t.Fatalf("submit-correction refresh note must not reopen blocker/question exit %q, got %q", forbidden, errorText)
		}
	}

	if strings.Contains(errorText, "Do not answer <<QUESTION>>") && !strings.Contains(errorText, "submit corrected edits now") {
		t.Fatalf("submit-correction refresh note must not reopen blocker/question exits, got %q", errorText)
	}
}

func TestPlanThenExecutePlanningStateTransitionsFromAnchorRefreshToSubmitCorrection(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/workflows/productworkflow/coverage_gate.go"},
		AllowedTools:      []string{"read_file", "code_navigate", "submit_plan"},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n\n1515:func runProductTDD() {}\n",
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read EvaluateCoverageGate in internal/workflows/productworkflow/coverage_gate.go lines 1-120\n\n1:func EvaluateCoverageGate() {}\n",
		},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "missing required edit path(s): `internal/workflows/productworkflow/coverage_gate.go`; invalid edit anchor(s): `cmd/idiom/product_workflow.go: old_text not found in current source`.",
		},
	}

	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode != PlanExecutePlanningModeAnchorRefresh {
		t.Fatalf("Mode = %q, want anchor refresh", state.Mode)
	}

	if len(state.RefreshPaths) != 1 || state.RefreshPaths[0] != "cmd/idiom/product_workflow.go" {
		t.Fatalf("RefreshPaths = %#v, want only invalid-anchor path", state.RefreshPaths)
	}

	if !slices.Contains(state.CallableTools, "read_file") || !slices.Contains(state.CallableTools, "code_navigate") || slices.Contains(state.CallableTools, "submit_plan") {
		t.Fatalf("anchor refresh callable tools should be read-only refresh tools, got %#v", state.CallableTools)
	}

	if !slices.Contains(state.AllowedActionClasses, actionRead) ||
		!slices.Contains(state.AllowedActionClasses, actionSourceSatisfied) ||
		!slices.Contains(state.AllowedActionClasses, actionBlocker) ||
		!slices.Contains(state.TerminalResponseShapes, "Decision: source-satisfied") ||
		!slices.Contains(state.TerminalResponseShapes, "<<QUESTION>>:") {

		t.Fatalf("anchor refresh should advertise read + source-satisfied + blocker action classes, actions=%#v terminal=%#v", state.AllowedActionClasses, state.TerminalResponseShapes)
	}

	if slices.Contains(state.TerminalOptions, "submit_plan") || planThenExecuteTerminalSubmitPlanAllowed(brief, results) {
		t.Fatalf("anchor refresh must not allow terminal submit_plan, terminal=%#v", state.TerminalOptions)
	}

	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief:             brief,
		Results:           results,
		BaseAllowedTools:  brief.AllowedTools,
		TextualToolParser: true,
		ToolTransportMode: "textual",
	})
	if contract.PhaseID != turnPhaseStaleAnchorRefresh ||
		!slices.Contains(contract.ActionPolicy.AllowedActionClasses, actionRead) ||
		!slices.Contains(contract.ActionPolicy.AllowedActionClasses, actionSourceSatisfied) ||
		!slices.Contains(contract.ActionPolicy.AllowedActionClasses, actionBlocker) ||
		slices.Contains(contract.ActionPolicy.AllowedActionClasses, actionSubmitPlan) {

		t.Fatalf("contract should advertise anchor-refresh read + source-satisfied + blocker actions, phase=%q actions=%#v", contract.PhaseID, contract.ActionPolicy.AllowedActionClasses)
	}

	withRefresh := append(append([]workspace.ToolResult(nil), results...), workspace.ToolResult{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n1515:func runProductTDD() {}",
	})

	state = DerivePlanExecutePlanningState(brief, withRefresh, PlanExecutePlanningTransport{TextualToolParser: true})

	if state.Mode != PlanExecutePlanningModeSubmitCorrection {
		t.Fatalf("Mode after refresh = %q, want submit correction", state.Mode)
	}

	if !slices.Contains(state.CallableTools, "submit_plan") || slices.Contains(state.CallableTools, "read_file") {
		t.Fatalf("submit correction callable tools should be submit_plan only, got %#v", state.CallableTools)
	}
}

func TestPlanThenExecutePostReadinessAnchorRefreshWinsOverPendingHistory(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go"},
		RequiredEvidence:  []string{requiredEvidenceGitHistoryRationale},
		AllowedTools:      []string{"read_file", "code_navigate", "history_range", "history_symbol", "submit_plan"},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n\n1515:func runProductTDD() {}\n",
		},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "invalid edit anchor(s): `cmd/idiom/product_workflow.go: old_text not found in current source`.",
		},
	}

	_, _, reject := planThenExecutePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "cmd/idiom/product_workflow.go",
		StartLine: 1515,
		EndLine:   1640,
	}, brief, results)
	if reject {
		t.Fatal("bounded anchor refresh should remain allowed even while git history is pending")
	}

	summary, errorText, reject := planThenExecutePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:      "history_range",
		Path:      "cmd/idiom/product_workflow.go",
		StartLine: 1,
		EndLine:   100,
	}, brief, results)
	if !reject {
		t.Fatal("history request should be rejected while anchor refresh remains")
	}

	if summary != "Bounded submit_plan anchor refresh required" {
		t.Fatalf("summary = %q, want anchor refresh", summary)
	}

	if !strings.Contains(errorText, "cmd/idiom/product_workflow.go") || strings.Contains(errorText, "Git history evidence required") {
		t.Fatalf("history request should preserve anchor-refresh guidance, got %q", errorText)
	}
}

func TestPlanThenExecutePostReadinessRecoveryKeepsHistoryToolsAfterAnchorRefresh(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go"},
		RequiredEvidence:  []string{requiredEvidenceGitHistoryRationale},
		AllowedTools:      []string{"read_file", "code_navigate", "history_range", "history_symbol", "submit_plan"},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n\n1515:func runProductTDD() {}\n",
		},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "invalid edit anchor(s): `cmd/idiom/product_workflow.go: old_text not found in current source`.",
		},
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n1515:func runProductTDD() {}",
		},
	}
	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})

	if state.Mode != PlanExecutePlanningModeHistoryPending {
		t.Fatalf("Mode after anchor refresh = %q, want history pending", state.Mode)
	}

	opts := planThenExecutePostReadinessRecoveryChatOptionsForBackend(llama.ChatOptions{
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "history_range"}},
			{Type: "function", Function: llama.ToolFunction{Name: "history_symbol"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "auto",
	}, brief, results, true)
	if opts.ToolChoice != "required" {
		t.Fatalf("ToolChoice = %q, want required", opts.ToolChoice)
	}

	if len(opts.Tools) != 2 ||
		!chatOptionsExposeTool(opts, "history_range") ||
		!chatOptionsExposeTool(opts, "history_symbol") ||
		chatOptionsExposeTool(opts, "submit_plan") {

		t.Fatalf("history-pending recovery should expose only history tools, got %#v", opts.Tools)
	}
}

func TestPlanThenExecutePostReadinessRecoverySkipsMissingPlanPathRefreshWithSourceBodyEvidence(t *testing.T) {
	thinkingOn := true
	opts := planThenExecutePostReadinessRecoveryChatOptions(llama.ChatOptions{
		EnableThinking:   &thinkingOn,
		PreserveThinking: true,
		MaxTokens:        4096,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "search_content"}},
			{Type: "function", Function: llama.ToolFunction{Name: "code_navigate"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "auto",
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/workflows/productworkflow/coverage_gate.go"},
	}, []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 cmd/idiom/product_workflow.go lines 1-20"},
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "Read EvaluateCoverageGate in internal/workflows/productworkflow/coverage_gate.go lines 1-80"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "missing required edit path(s): `internal/workflows/productworkflow/coverage_gate.go`.",
		},
	})

	if opts.ToolChoice != "required" {
		t.Fatalf("source-backed missing-path recovery should force submit_plan tool choice, got %q", opts.ToolChoice)
	}

	if len(opts.Tools) != 1 || !chatOptionsExposeTool(opts, "submit_plan") || chatOptionsExposeTool(opts, "read_file") || chatOptionsExposeTool(opts, "code_navigate") {
		t.Fatalf("source-backed missing-path recovery should expose only submit_plan, got %#v", opts.Tools)
	}

	if opts.EnableThinking == nil || *opts.EnableThinking {
		t.Fatalf("source-backed missing-path recovery should force visible corrected submit_plan output, got %#v", opts.EnableThinking)
	}

	if !opts.ChatTemplateKwargs {
		t.Fatalf("source-backed missing-path recovery should force request-level thinking kwargs")
	}
}

func TestPlanThenExecutePostReadinessRecoveryRequiresSubmitForOutlineOnlyMissingCoverage(t *testing.T) {
	thinkingOn := true
	opts := planThenExecutePostReadinessRecoveryChatOptions(llama.ChatOptions{
		EnableThinking:   &thinkingOn,
		PreserveThinking: true,
		MaxTokens:        4096,
		Tools: []llama.Tool{
			{Type: "function", Function: llama.ToolFunction{Name: "read_file"}},
			{Type: "function", Function: llama.ToolFunction{Name: "search_content"}},
			{Type: "function", Function: llama.ToolFunction{Name: "code_navigate"}},
			{Type: "function", Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		ToolChoice: "auto",
	}, workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/workflows/productworkflow/coverage_gate.go"},
	}, []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 cmd/idiom/product_workflow.go lines 1-20"},
		{Tool: "code_navigate", Summary: "Code navigation routed file_outline via go_file_outline", Output: "route_summary: Outlined 3 top-level declaration(s) in internal/workflows/productworkflow/coverage_gate.go."},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "missing required edit path(s): `internal/workflows/productworkflow/coverage_gate.go`.",
		},
	})

	if opts.ToolChoice != "required" {
		t.Fatalf("outline-only missing-coverage recovery should force submit_plan tool choice, got %q", opts.ToolChoice)
	}

	if len(opts.Tools) != 1 || !chatOptionsExposeTool(opts, "submit_plan") || chatOptionsExposeTool(opts, "read_file") || chatOptionsExposeTool(opts, "code_navigate") || chatOptionsExposeTool(opts, "search_content") {
		t.Fatalf("outline-only missing-coverage recovery should expose only submit_plan, got %#v", opts.Tools)
	}

	if opts.EnableThinking == nil || *opts.EnableThinking {
		t.Fatalf("outline-only missing-coverage recovery should force visible corrected submit_plan output, got %#v", opts.EnableThinking)
	}

	if !opts.ChatTemplateKwargs {
		t.Fatalf("outline-only missing-coverage recovery should force request-level thinking kwargs")
	}
}

func TestPlanExecuteRequiredAnchorRefreshRedirectsUnboundedReadFile(t *testing.T) {
	runner := &Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{
			"native/src/core/colony_sim_tick.cpp",
			"native/src/simulation/world/collapse_event.cpp",
			"native/src/simulation/world/collapse_event.h",
			"native/tests/test_collapse_event.cpp",
		},
	}}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f3 native/src/simulation/world/collapse_event.h lines 1-600 (total lines: 600)"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected",
			Error:   "edits[0]: whole-file replace_all edit would rewrite existing source file /Users/ben/Documents/GitHub/Colony/native/src/simulation/world/collapse_event.h; use a bounded anchored edit with old_text/new_text or start_line/end_line/content",
		},
	}
	req := workspace.ToolRequest{
		Tool: workspace.ToolNameReadFile,
		Path: "native/src/simulation/world/collapse_event.h",
	}

	redirected, _, ok := runner.planThenExecuteRejectedPlanRefreshReadRedirect(req, results)
	if !ok {
		t.Fatal("expected unbounded required-path anchor refresh read_file to redirect to bounded fallback")
	}

	if redirected.Tool != workspace.ToolNameReadFile || redirected.Path != "native/src/simulation/world/collapse_event.h" {
		t.Fatalf("unexpected redirect target: %#v", redirected)
	}

	if redirected.StartLine <= 0 || redirected.EndLine < redirected.StartLine || redirected.EndLine-redirected.StartLine+1 > 400 {
		t.Fatalf("redirect must be a bounded anchor-refresh read, got %#v", redirected)
	}

	if !planThenExecuteRejectedPlanSourceRefreshAllowed(redirected, runner.RuntimeBrief.RequiredEditPaths, results) {
		t.Fatalf("redirected required-path refresh should be allowed after rejected submit_plan: %#v", redirected)
	}
}

func TestPlanExecuteRequiredAnchorRefreshRedirectsRepeatedBoundedReadFile(t *testing.T) {
	runner := &Runner{RuntimeBrief: workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"native/src/core/colony_sim_tick.cpp"},
	}}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f3 native/src/core/colony_sim_tick.cpp lines 1-120 (total lines: 2616)"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "invalid edit anchor(s): `native/src/core/colony_sim_tick.cpp: old_text not found in current source`.",
		},
		{Tool: "read_file", Summary: "Read f3 native/src/core/colony_sim_tick.cpp lines 1-120 (total lines: 2616)"},
	}
	req := workspace.ToolRequest{
		Tool:      workspace.ToolNameReadFile,
		Path:      "native/src/core/colony_sim_tick.cpp",
		StartLine: 1,
		EndLine:   120,
	}

	if planThenExecuteRejectedPlanSourceRefreshAllowed(req, runner.RuntimeBrief.RequiredEditPaths, results) {
		t.Fatal("repeated bounded anchor-refresh window should not be accepted as new refresh evidence")
	}

	redirected, _, ok := runner.planThenExecuteRejectedPlanRefreshReadRedirect(req, results)
	if !ok {
		t.Fatal("expected repeated bounded anchor-refresh read_file to redirect to the next bounded window")
	}

	if redirected.Path != "native/src/core/colony_sim_tick.cpp" || redirected.StartLine <= 120 || redirected.EndLine < redirected.StartLine || redirected.EndLine-redirected.StartLine+1 > 400 {
		t.Fatalf("redirect should advance to a later bounded window, got %#v", redirected)
	}

	if !planThenExecuteRejectedPlanSourceRefreshAllowed(redirected, runner.RuntimeBrief.RequiredEditPaths, results) {
		t.Fatalf("advanced refresh window should be allowed after repeated bounded read: %#v", redirected)
	}
}

func TestPlanThenExecuteRequiredPathDiscoveryRejectsOutOfScopeInvalidAnchorRefresh(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/workflows/productworkflow/coverage_gate.go"},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n\n1515:func runProductTDD() {}\n",
		},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "invalid edit anchor(s): `docs/out-of-scope.md: old_text not found in current source`; path(s) outside required_edit_paths: `docs/out-of-scope.md`.",
		},
	}

	_, errorText, reject := planThenExecuteRequiredPathDiscoveryRejection(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "docs/out-of-scope.md",
		StartLine: 1,
		EndLine:   20,
	}, brief, results)
	if !reject {
		t.Fatal("invalid-anchor recovery must not unlock out-of-scope reads")
	}

	if !strings.Contains(errorText, "outside required_edit_paths") {
		t.Fatalf("rejection should explain out-of-scope anchor, got %q", errorText)
	}
}

func TestPlanThenExecutePostReadinessDiscoveryAllowsUnreadPathRead(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"cmd/idiom/product_workflow.go", "internal/workflows/productworkflow/coverage_gate.go"},
	}
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output:  "route_summary: Read runProductTDD in cmd/idiom/product_workflow.go lines 1515-1640\n\n1515:func runProductTDD() {}\n",
	}}
	_, _, reject := planThenExecutePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool: "read_file",
		Path: "internal/workflows/productworkflow/coverage_gate.go",
	}, brief, results)

	if reject {
		t.Fatal("required path reads should remain allowed before planner readiness")
	}
}

func TestPlanThenExecutePlannerToolGuardRejectsShellAndVerification(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
		},
	}
	for _, tool := range []string{"shell", "git_diff", "git_status", "source_sanity", "affected_suites", "context_trace_pack", "task", "task_ledger", "pwd"} {
		got := runner.preflightPlanThenExecutePlannerToolGuard(workspace.ToolRequest{Tool: tool})
		if !got.Intercepted {
			t.Fatalf("planner phase must reject %q, got %#v", tool, got)
		}

		if !strings.Contains(got.Result.Error, "current planner terminal contract") || !strings.Contains(got.Result.Error, "submit_plan") {
			t.Fatalf("rejection should point at the active planner contract, got %q", got.Result.Error)
		}

		for _, forbidden := range []string{"Decision: source-satisfied", "<<QUESTION>>"} {
			if strings.Contains(got.Result.Error, forbidden) {
				t.Fatalf("planner tool rejection advertised stale terminal exit %q: %q", forbidden, got.Result.Error)
			}
		}
	}
}

func TestPlanThenExecuteClampedEOFReadRejectsRepeatedOutOfRangeWindow(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f4 cmd/idiom/product_workflow.go lines 2705-2805 (requested range 3700-3800 was clamped to file bounds; file has 2805 lines total)",
		Output:  "2705:tail",
	}}
	summary, errorText, reject := runner.planThenExecuteClampedEOFReadRejection(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "cmd/idiom/product_workflow.go",
		StartLine: 3800,
		EndLine:   3900,
	}, results)

	if !reject {
		t.Fatal("expected repeated read_file beyond known EOF to be rejected")
	}

	if summary != "Out-of-range read_file rejected" || !strings.Contains(errorText, "submit_plan") || !strings.Contains(errorText, "2805 lines total") {
		t.Fatalf("rejection should explain the EOF fact and valid planner closeouts, summary=%q error=%q", summary, errorText)
	}
}

func TestPlanThenExecuteClampedEOFReadAllowsUsefulBoundedWindow(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read target.txt lines 1-3 (requested range 1-30 was clamped to file bounds; file has 3 lines total)",
		Output:  "1:a",
	}}
	_, _, reject := runner.planThenExecuteClampedEOFReadRejection(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "target.txt",
		StartLine: 2,
		EndLine:   3,
	}, results)

	if reject {
		t.Fatal("a bounded read that starts before known EOF can still add evidence")
	}
}

func TestPlanThenExecuteHardRedirectDoesNotHideReadFileWithRequiredAnchors(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			RequiredEditPaths: []string{"cmd/idiom/product_workflow.go"},
		},
	}
	if runner.shouldHideToolAfterDiscoveryHardRedirect(workspace.ToolRequest{
		Tool: "read_file",
		Path: "cmd/idiom/main.go",
	}) {

		t.Fatal("plan-execute planner must keep read_file available for later exact-anchor reads on required edit paths")
	}

	if !runner.shouldHideToolAfterDiscoveryHardRedirect(workspace.ToolRequest{
		Tool: "search_content",
		Path: "cmd/idiom/main.go",
	}) {

		t.Fatal("non-anchor read tools can still be hidden after hard redirect")
	}
}

func TestPlanThenExecutePlannerToolGuardDoesNotAffectReviewRepair(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecuteReviewRepairTaskFamily,
		},
	}
	if got := runner.preflightPlanThenExecutePlannerToolGuard(workspace.ToolRequest{Tool: "git_diff"}); got.Intercepted {
		t.Fatalf("review-repair may inspect diff evidence; planner-only guard must stand down, got %#v", got)
	}
}

func TestReviewRepairCompletionGateRejectsNonDecisionFinalAnswer(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
			AllowedTools:      []string{"submit_plan"},
		},
	}
	got, rejected := runner.completionGateRejection("Let me trace the actual implementation to verify the tests against real code.", nil, 1, nil, false)

	if !rejected {
		t.Fatal("review-repair must reject process narration as a final answer")
	}

	if got.Tool != "completion_gate" || !strings.Contains(got.Error, "Decision: pass") || !strings.Contains(got.Error, "submit_plan") {
		t.Fatalf("review-repair rejection should name the valid closeout shapes, got %#v", got)
	}
}

func TestReviewRepairCompletionGateAcceptsPassChecklistOrSubmittedPlan(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
			AllowedTools:      []string{"submit_plan"},
		},
	}
	passAnswer := "Decision: pass\n\nVerification Checklist:\n- Required behavior is satisfied.\n- Scope safety checked."

	if got, rejected := runner.completionGateRejection(passAnswer, nil, 1, nil, false); rejected {
		t.Fatalf("review-repair pass checklist should be accepted, got %#v", got)
	}

	pathOnlyChecklist := "Decision: pass\n\nVerification Checklist:\n- `widget_config.json`\n- `widget_notes.md`\n"
	if got, rejected := runner.completionGateRejection(pathOnlyChecklist, nil, 1, nil, false); rejected {
		t.Fatalf("review-repair pass checklist must not be rejected by the generic read-only explanation gate, got %#v", got)
	}

	recoveryTemplate := "The completion gate rejected the review-repair response.\n\nPass shape:\nDecision: pass\n\nVerification Checklist:\n- Required behavior: <checked fact>."
	if got, rejected := runner.completionGateRejection(recoveryTemplate, nil, 1, nil, false); !rejected {
		t.Fatalf("review-repair must not accept recovery template text as a pass, got %#v", got)
	}

	results := []workspace.ToolResult{{Tool: "submit_plan", Summary: "Plan submitted with 1 edit(s); the executor phase will dispatch them."}}
	if got, rejected := runner.completionGateRejection("Plan submitted with 1 edit.", results, 2, nil, false); rejected {
		t.Fatalf("review-repair submitted corrective plan should be accepted, got %#v", got)
	}
}

func TestReviewRepairCompletionGateRejectsPassWhenPhaseEvidenceFailed(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
			Readiness: &workerruntime.ReadinessState{
				OpenObligations: []string{"verification_failed:go test ./..."},
			},
		},
	}
	passAnswer := "Decision: pass\n\nVerification Checklist:\n- Required behavior is satisfied.\n- Test evidence passed."
	got, rejected := runner.completionGateRejection(passAnswer, nil, 1, nil, false)

	if !rejected {
		t.Fatal("review-repair pass must be rejected while phase evidence has failed verification")
	}

	if got.Tool != "completion_gate" || !strings.Contains(got.Error, "verification_failed:go test ./...") || !strings.Contains(got.Error, "submit_plan") {
		t.Fatalf("rejection should name failed obligation and corrective plan path, got %#v", got)
	}

	if got, rejected := runner.completionGateRejection("<<QUESTION>>: verification failed outside allowed repair paths", nil, 1, nil, false); rejected {
		t.Fatalf("typed blocker should remain accepted with failed phase evidence, got %#v", got)
	}

	results := []workspace.ToolResult{{Tool: "submit_plan", Summary: "Plan submitted with 1 edit(s); the executor phase will dispatch them."}}
	if got, rejected := runner.completionGateRejection("Plan submitted with 1 edit.", results, 2, nil, false); rejected {
		t.Fatalf("corrective submit_plan should remain accepted with failed phase evidence, got %#v", got)
	}
}

func TestReviewRepairDoesNotTriggerReadOnlyAnswerReady(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			Contract:          "review_findings",
			PermissionProfile: "plan_submission_review",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read cmd/idiom/product_workflow.go lines 280-360 (total lines: 2961)",
			Output:  "280:func runProductLint() error { return nil }",
		},
		{
			Tool:    "read_file",
			Summary: "Read cmd/idiom/product_workflow.go lines 1-50 (total lines: 2961)",
			Output:  "1:package main",
		},
	}

	if runner.readOnlyAnalysisAnswerReadyAfterResult(workspace.ToolRequest{Tool: "read_file"}, results[1], results) {
		t.Fatal("review-repair is a plan-submission phase and must not receive generic read-only answer-ready nudges")
	}
}
