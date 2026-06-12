package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

func TestExplorationDelegationNudgeUsesReadLoopState(t *testing.T) {
	root := t.TempDir()
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
	}

	if got := runner.explorationDelegationNudge(1, nil); got != "" {
		t.Fatalf("expected no nudge before threshold, got %q", got)
	}

	evidence := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read README.md", Output: "one"},
		{Tool: "search_files", Summary: "Matched 1 file", Output: "README.md"},
	}
	if got := runner.explorationDelegationNudge(4, evidence); !strings.Contains(got, "you may delegate one bounded read_only task") {
		t.Fatalf("expected delegation nudge at threshold, got %q", got)
	}

	done := []workspace.ToolResult{{Tool: "task", Summary: "Child task completed"}}
	if got := runner.explorationDelegationNudge(6, done); got != "" {
		t.Fatalf("expected no nudge after delegated scout, got %q", got)
	}

	runner.RuntimeBrief = workerruntime.WorkerRuntimeBrief{AllowedTools: []string{"read_file", "search_files", "search_content"}}
	if got := runner.explorationDelegationNudge(6, evidence); got != "" {
		t.Fatalf("expected no nudge when task tool is not available under runtime brief, got %q", got)
	}

	if !isReadClassTool(workspace.ToolRequest{Tool: "discovery_query", Purpose: "impact_analysis"}) {
		t.Fatal("discovery_query should count as read-class discovery")
	}

	for _, tool := range []string{"file_stats", "context_impact", "harness_audit"} {
		if !isReadClassTool(workspace.ToolRequest{Tool: tool}) {
			t.Fatalf("%s should count as read-class inspection", tool)
		}
	}

	runner.RuntimeBrief = workerruntime.WorkerRuntimeBrief{
		Contract:     "direct_answer",
		AllowedTools: []string{"task", "read_file", "search_files", "search_content"},
	}

	directAnswerEvidence := []workspace.ToolResult{
		{Tool: "search_content", Summary: "Found 3 matching line(s)", Output: "README.md:1:subagent"},
		{Tool: "search_files", Summary: "Matched 1 file", Output: "README.md"},
	}

	if got := runner.explorationDelegationNudge(1, directAnswerEvidence); got != "" {
		t.Fatalf("expected no direct-answer nudge before tighter threshold, got %q", got)
	}

	if got := runner.explorationDelegationNudge(2, directAnswerEvidence); !strings.Contains(got, "you may delegate one bounded read_only task") {
		t.Fatalf("expected direct-answer nudge at tighter threshold, got %q", got)
	}

	concreteEvidence := append([]workspace.ToolResult(nil), directAnswerEvidence...)

	concreteEvidence = append(concreteEvidence, workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read internal/evidence/transcript/debuglog_test.go lines 1-80",
		Output:  "package transcript",
	})

	if got := runner.explorationDelegationNudge(3, concreteEvidence); got != "" {
		t.Fatalf("expected concrete file inspection to suppress direct-answer delegation, got %q", got)
	}

	runner.RuntimeBrief = workerruntime.WorkerRuntimeBrief{
		Contract:     "architecture_assessment",
		AllowedTools: []string{"task", "read_file", "search_files", "search_content"},
		SearchPolicy: []string{"prefer_discovery_query_for_architecture_boundary", "trace_boundaries_from_named_components"},
	}
	if got := runner.explorationDelegationNudge(1, evidence); got != "" {
		t.Fatalf("expected architecture lane to avoid early delegation bias, got %q", got)
	}
}

func TestArchitectureToolBudgetCountsOnlySuccessfulResults(t *testing.T) {
	tests := []struct {
		name     string
		results  []workspace.ToolResult
		maxTools int
		want     bool
	}{
		{
			name:     "4 successful source reads triggers budget",
			maxTools: 4,
			results: []workspace.ToolResult{
				{Tool: "read_file", Summary: "Read internal/runtime/agent/runner.go lines 1-80 (total lines: 21636)", Output: "package main"},
				{Tool: "read_file", Summary: "Read internal/runtime/agent/runner.go lines 81-160 (total lines: 21636)", Output: "func main()"},
				{Tool: "read_file", Summary: "Read internal/runtime/agent/runner.go lines 161-240 (total lines: 21636)", Output: "var x int"},
				{Tool: "read_file", Summary: "Read internal/runtime/agent/runner.go lines 241-320 (total lines: 21636)", Output: "func helper()"},
			},
			want: true,
		},
		{
			name:     "3 successful + 1 rejected still triggers budget at 4",
			maxTools: 4,
			results: []workspace.ToolResult{
				{Tool: "read_file", Summary: "Read internal/runtime/agent/runner.go lines 1-80 (total lines: 21636)", Output: "package main"},
				{Tool: "read_file", Summary: "Read internal/runtime/agent/runner.go lines 81-160 (total lines: 21636)", Output: "func main()"},
				{Tool: "read_file", Summary: "Read internal/runtime/agent/runner.go lines 161-240 (total lines: 21636)", Output: "var x int"},
				{Tool: "read_file", Error: "file not found", Summary: "Read rejected"},
			},
			want: true,
		},
		{
			name:     "3 successful + 2 rejected still triggers budget at 4",
			maxTools: 4,
			results: []workspace.ToolResult{
				{Tool: "read_file", Summary: "Read internal/runtime/agent/runner.go lines 1-80 (total lines: 21636)", Output: "package main"},
				{Tool: "read_file", Summary: "Read internal/runtime/agent/runner.go lines 81-160 (total lines: 21636)", Output: "func main()"},
				{Tool: "read_file", Summary: "Read internal/runtime/agent/runner.go lines 161-240 (total lines: 21636)", Output: "var x int"},
				{Tool: "read_file", Error: "file not found", Summary: "Read rejected"},
				{Tool: "code_navigate", Error: "symbol not found", Summary: "Symbol not found"},
			},
			want: true,
		},
		{
			name:     "5 results with 4 successful triggers budget",
			maxTools: 4,
			results: []workspace.ToolResult{
				{Tool: "read_file", Summary: "Read internal/runtime/agent/runner.go lines 1-80 (total lines: 21636)", Output: "package main"},
				{Tool: "read_file", Summary: "Read internal/runtime/agent/runner.go lines 81-160 (total lines: 21636)", Output: "func main()"},
				{Tool: "read_file", Error: "file not found", Summary: "Read rejected"},
				{Tool: "read_file", Summary: "Read internal/runtime/agent/runner.go lines 161-240 (total lines: 21636)", Output: "var x int"},
				{Tool: "read_file", Summary: "Read internal/runtime/agent/runner.go lines 241-320 (total lines: 21636)", Output: "func helper()"},
			},
			want: true,
		},
		{
			name:     "fewer results than maxTools does not trigger",
			maxTools: 4,
			results: []workspace.ToolResult{
				{Tool: "read_file", Summary: "Read internal/runtime/agent/runner.go lines 1-80 (total lines: 21636)", Output: "package main"},
				{Tool: "read_file", Summary: "Read internal/runtime/agent/runner.go lines 81-160 (total lines: 21636)", Output: "func main()"},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := readOnlyArchitectureToolBudgetCloseoutApplies(
				workerruntime.WorkerRuntimeBrief{
					PermissionProfile: string(workerruntime.PermissionReadOnlyWorkspace),
					Contract:          string(workerruntime.ContractArchitecture),
					Objective:         "How does subagent work?",
				},
				tt.results,
				tt.maxTools,
			)
			if got != tt.want {
				t.Fatalf("readOnlyArchitectureToolBudgetCloseoutApplies = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPostChildReadBudgetForDirectAnswerIsZero(t *testing.T) {
	if got := postChildReadBudgetForBrief(workerruntime.WorkerRuntimeBrief{Contract: "direct_answer"}); got != 0 {
		t.Fatalf("direct-answer post-child budget = %d, want 0", got)
	}

	if got := postChildReadBudgetForBrief(workerruntime.WorkerRuntimeBrief{Contract: "grounded_explanation", Constraints: []string{"thoroughness=standard"}}); got != 1 {
		t.Fatalf("standard read-only post-child budget = %d, want 1", got)
	}

	if got := postChildReadBudgetForBrief(workerruntime.WorkerRuntimeBrief{Contract: "grounded_explanation", Constraints: []string{"thoroughness=high"}}); got != 2 {
		t.Fatalf("high read-only post-child budget = %d, want 2", got)
	}

	if got := postChildReadBudgetForBrief(workerruntime.WorkerRuntimeBrief{Contract: "grounded_explanation"}); got != 2 {
		t.Fatalf("unspecified read-only post-child budget = %d, want 2", got)
	}
}

func TestExplorationDelegationNudgeSkipsMutationLanes(t *testing.T) {
	root := t.TempDir()
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: "explore_then_implement",
			Contract:   "implementation_result",
		},
	}
	evidence := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read docs/frontier-routing-implementation-plan.md", Output: "one"},
		{Tool: "go_workspace_symbol", Summary: "Resolved Go workspace symbols", Output: "two"},
	}

	if got := runner.explorationDelegationNudge(6, evidence); got != "" {
		t.Fatalf("expected no delegation nudge for mutation lane, got %q", got)
	}
}

func TestMutationCompletionGateRequiresWriteAndVerification(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.RuntimeBriefForRole("implement", "main", "update README"),
	}
	if result, rejected := runner.mutationCompletionRejection("Done.", nil); !rejected || result.Summary == "" {
		t.Fatalf("expected no-write mutation rejection, got rejected=%v result=%#v", rejected, result)
	}

	if result, rejected := runner.mutationCompletionRejection("Already done.", nil); !rejected || result.Summary == "" {
		t.Fatalf("expected vague already-done answer rejection, got rejected=%v result=%#v", rejected, result)
	}

	falseCompletion := strings.Join([]string{
		"**Outcome:** `internal/labels/labels.go` has been edited so `ShortLabel()` returns `\"Save\"` instead of `\"Save changes\"`.",
		"",
		"**Verification:** Source read confirms the edit was applied correctly.",
		"",
		"**Remaining risk/blocker:** The verification plan calls for `go test ./...`, but `shell` is forbidden.",
	}, "\n")
	if result, rejected := runner.mutationCompletionRejection(falseCompletion, []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 internal/labels/labels.go lines 1-5", Output: "func ShortLabel() string { return \"Save changes\" }"},
	}); !rejected || !strings.Contains(result.Error, "no successful write tool result") {
		t.Fatalf("expected claimed edit without write to be rejected, got rejected=%v result=%#v", rejected, result)
	}

	// Source-file write (post-#74 lane-aware gate): verification is
	// required for source paths. Markdown writes are exempt — see
	// TestCompletionGateExemptsNonSourceWritesFromVerification.
	write := workspace.ToolResult{Tool: "edit_file", Summary: "Edited foo.go"}
	if result, rejected := runner.mutationCompletionRejection("Done.", []workspace.ToolResult{write}); !rejected || !strings.Contains(result.Error, "verification") {
		t.Fatalf("expected missing-verification rejection, got rejected=%v result=%#v", rejected, result)
	}

	// Source-file edits need source-aware sanity (source_sanity), not
	// just patch hygiene (`git diff --check`). Post-#74 the gate
	// distinguishes these: patch hygiene alone is insufficient for
	// source paths.
	verify := workspace.ToolResult{Tool: "source_sanity", Summary: "Source sanity passed", Output: "foo.go: no warnings or errors via go_diagnostics"}
	if result, rejected := runner.mutationCompletionRejection("Done.", []workspace.ToolResult{write, verify}); rejected {
		t.Fatalf("expected verified mutation to pass, got %#v", result)
	}

	unsupportedPathAnswer := "Changed paths:\n- README.md\n- src/missing.go\n\nVerification: git diff --check passed."
	if result, rejected := runner.mutationCompletionRejection(unsupportedPathAnswer, []workspace.ToolResult{write, verify}); !rejected || !strings.Contains(result.Error, "not backed") {
		t.Fatalf("expected unsupported changed-path claim rejection, got rejected=%v result=%#v", rejected, result)
	}

	unsupportedChangesMadeAnswer := "Changes made:\n\n1. **`README.md`** updated.\n2. **`src/missing.go`** updated.\n\nVerification: git diff --check passed."
	if result, rejected := runner.mutationCompletionRejection(unsupportedChangesMadeAnswer, []workspace.ToolResult{write, verify}); !rejected || !strings.Contains(result.Error, "src/missing.go") {
		t.Fatalf("expected unsupported changes-made claim rejection, got rejected=%v result=%#v", rejected, result)
	}

	// Stale-verification: a NEW source-file write after the verify
	// invalidates it (must re-verify). Use a source file so the gate
	// fires; .md writes are exempt post-#74.
	laterWrite := workspace.ToolResult{Tool: "edit_file", Summary: "Edited bar.go"}
	if result, rejected := runner.mutationCompletionRejection("Done.", []workspace.ToolResult{write, verify, laterWrite}); !rejected || !strings.Contains(result.Error, "latest successful write") {
		t.Fatalf("expected stale-verification mutation rejection, got rejected=%v result=%#v", rejected, result)
	}

	if _, rejected := runner.mutationCompletionRejection("No change needed; already set.", nil); rejected {
		t.Fatal("expected explicit no-op answer to pass mutation gate")
	}
}

func TestRunnerReadinessStateTreatsContextPacketAsWriteReadyEvidence(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"native/tests/test_transport_system.cpp"},
		TargetPacket:      []string{"prompt_context:context_packet", "prompt_context:verified_current_state"},
		EvidencePaths:     []string{"native/src/simulation/transport/route_graph.cpp"},
	}
	readiness := runnerReadinessState("main", brief, nil)

	if readiness == nil {
		t.Fatal("expected readiness state")
	}

	if !readiness.ReadyToWrite || readiness.ReadyToAnswer || readiness.ReadyToVerify {
		t.Fatalf("readiness flags = %#v, want write-ready only", readiness)
	}

	for _, want := range []string{"context_packet", "required_edit_paths", "evidence_paths"} {
		if !slices.Contains(readiness.EvidenceSources, want) {
			t.Fatalf("expected evidence source %q, got %#v", want, readiness.EvidenceSources)
		}
	}

	if !slices.Contains(readiness.ClosedClaims, "context_packet_available") {
		t.Fatalf("expected context packet closed claim, got %#v", readiness.ClosedClaims)
	}

	if !slices.Contains(readiness.OpenObligations, "required_edit_paths_not_changed") {
		t.Fatalf("expected required-path open obligation, got %#v", readiness.OpenObligations)
	}

	for _, want := range []string{"semantic/context/declared project discovery", "named_gap", "<<QUESTION>>"} {
		if !strings.Contains(readiness.FurtherReadsRequire, want) {
			t.Fatalf("expected scope-ready read requirement %q, got %q", want, readiness.FurtherReadsRequire)
		}
	}
}

func TestRunnerReadinessStateDoesNotTreatBarePacketMarkerAsWriteReady(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		TargetPacket:      []string{"prompt_context:current_state"},
	}
	readiness := runnerReadinessState("main", brief, nil)

	if readiness == nil {
		t.Fatal("expected readiness state to report packet presence")
	}

	if readiness.ReadyToWrite {
		t.Fatalf("bare packet marker without target/evidence paths should not be write-ready: %#v", readiness)
	}
}

func TestRunnerReadinessStateRequiresVerificationAfterSourceEdit(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:           "implementation",
		Contract:             "implementation_result",
		PermissionProfile:    "guarded_workspace_write",
		RequiredEditPaths:    []string{"internal/surfaces/tui/app/prompt_routing.go"},
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
	}
	results := []workspace.ToolResult{{
		Tool:    "edit_file",
		Summary: "Edited internal/surfaces/tui/app/prompt_routing.go",
		Output:  "Updated internal/surfaces/tui/app/prompt_routing.go",
	}}

	readiness := runnerReadinessState("main", brief, results)
	if readiness == nil {
		t.Fatal("expected readiness state")
	}

	if readiness.ReadyToAnswer || !readiness.ReadyToVerify {
		t.Fatalf("readiness flags = %#v, want verification-ready and not answer-ready", readiness)
	}

	if !slices.Contains(readiness.ClosedClaims, "required_edit_paths_changed") {
		t.Fatalf("expected required edit paths closed, got %#v", readiness.ClosedClaims)
	}

	if !slices.Contains(readiness.OpenObligations, "source_aware_verification_pending") {
		t.Fatalf("expected source-aware verification obligation, got %#v", readiness.OpenObligations)
	}
}

func TestRunnerReadinessStateRequiresPlannerOwnedLaneSanityAfterSourceEdit(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:           "implementation",
		Contract:             "implementation_result",
		PermissionProfile:    "guarded_workspace_write",
		RequiredEditPaths:    []string{"native/src/example.cpp"},
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
		Constraints:          []string{"planner_owned_verification"},
	}
	results := []workspace.ToolResult{{
		Tool:    "edit_file",
		Summary: "Edited native/src/example.cpp",
	}}

	readiness := runnerReadinessState("main", brief, results)
	if readiness == nil {
		t.Fatal("expected readiness state")
	}

	if readiness.ReadyToAnswer || readiness.ReadyToVerify {
		t.Fatalf("readiness flags = %#v, want pending planner-owned lane sanity", readiness)
	}

	if !slices.Contains(readiness.OpenObligations, "planner_owned_source_lane_sanity_pending") {
		t.Fatalf("expected planner-owned source lane sanity obligation, got %#v", readiness.OpenObligations)
	}
}

func TestRunnerReadinessStateRequiresPlannerOwnedSourceSanityAfterPatchHygiene(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:           "implementation",
		Contract:             "implementation_result",
		PermissionProfile:    "guarded_workspace_write",
		RequiredEditPaths:    []string{"native/src/example.cpp"},
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
		Constraints:          []string{"planner_owned_verification"},
	}
	results := []workspace.ToolResult{
		{Tool: "edit_file", Summary: "Edited native/src/example.cpp"},
		{Tool: "shell", Summary: "Ran shell command", Output: "$ git diff --check -- native/src/example.cpp\n"},
	}

	readiness := runnerReadinessState("main", brief, results)
	if readiness == nil {
		t.Fatal("expected readiness state")
	}

	if readiness.ReadyToAnswer || !readiness.ReadyToVerify {
		t.Fatalf("readiness flags = %#v, want source-sanity-ready and not answer-ready", readiness)
	}

	if !slices.Contains(readiness.OpenObligations, "planner_owned_source_lane_sanity_pending") {
		t.Fatalf("expected planner-owned source lane sanity obligation, got %#v", readiness.OpenObligations)
	}

	if !strings.Contains(readiness.FurtherReadsRequire, "source-sanity") {
		t.Fatalf("expected source-sanity follow-up boundary, got %q", readiness.FurtherReadsRequire)
	}
}

func TestCompletionGateExemptsNonSourceWritesFromVerification(t *testing.T) {
	cases := []struct {
		name      string
		writePath string
		required  []string
		want      bool
	}{
		{"doc-lane markdown write skips gate", "Wrote draft.md", []string{"draft.md"}, false},
		{"doc-lane txt write skips gate", "Wrote notes.txt", []string{"notes.txt"}, false},
		{"doc-lane go source write requires verification", "Wrote main.go", []string{"main.go"}, true},
		{"doc-lane yaml write requires verification (no exemption yet)", "Wrote config.yaml", []string{"config.yaml"}, true},
		{"implementation-lane markdown write skips gate", "Wrote README.md", nil, false},
		{"implementation-lane markdown with source required path skips gate for non-source write", "Wrote README.md", []string{"main.go"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			results := []workspace.ToolResult{
				{Tool: "write_file", Summary: c.writePath},
			}
			brief := workerruntime.WorkerRuntimeBrief{
				VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
				RequiredEditPaths:    c.required,
			}
			got := hasPendingVerificationAfterWrites(results, brief)

			if got != c.want {
				t.Fatalf("hasPendingVerificationAfterWrites for %q (required=%v) = %v, want %v", c.writePath, c.required, got, c.want)
			}
		})
	}
}

func TestDocDeliverableMkdirParentDoesNotRequireVerification(t *testing.T) {
	results := []workspace.ToolResult{
		{Tool: "mkdir", Summary: "Created directory .idiom/refine/example/iter-1"},
		{Tool: "write_file", Summary: "Wrote .idiom/refine/example/iter-1/draft.md"},
	}
	brief := workerruntime.WorkerRuntimeBrief{
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
		RequiredEditPaths:    []string{".idiom/refine/example/iter-1/draft.md"},
	}

	if hasPendingVerificationAfterWrites(results, brief) {
		t.Fatalf("doc-deliverable mkdir parent plus markdown write should not require source verification")
	}

	sourceBrief := brief

	sourceBrief.RequiredEditPaths = []string{".idiom/refine/example/iter-1/generated.go"}

	if !hasPendingVerificationAfterWrites(results, sourceBrief) {
		t.Fatalf("mkdir exemption must not apply when the required deliverable is source-like")
	}
}

func TestWritePathIsNonSource(t *testing.T) {
	cases := map[string]bool{
		"draft.md":        true,
		"docs/design.md":  true,
		"NOTES.txt":       true,
		"design.markdown": true,
		"README.rst":      true,
		"main.go":         false,
		"config.yaml":     false,
		"package.json":    false,
		"":                false,
		"weird":           false,
		"path/to/file.MD": true, // case-insensitive
	}
	for path, want := range cases {
		if got := writePathIsNonSource(path); got != want {
			t.Errorf("writePathIsNonSource(%q) = %v, want %v", path, got, want)
		}
	}
}

// TestImplementationPreWriteNonWriteRecoveryAllowsSubmitPlanInPlannerPhase
// asserts the fix for #91: when a plan-execute planner-phase lane has
// a write-ready packet, the pre-write non-write-tool gate must accept
// submit_plan (the planner's valid write surface). Before the fix,
// every comprehensive plan-execute brief was rejected because
// IsWorkspaceWriteTool doesn't recognize submit_plan.
func TestImplementationPreWriteNonWriteRecoveryAllowsSubmitPlanInPlannerPhase(t *testing.T) {
	r := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go", "b.go"},
			Constraints:       []string{"exact_anchor_implementation"},
			RequiredEditSnippets: []workerruntime.RequiredEditSnippet{
				{Path: "a.go", Content: "func foo() {}"},
			},
		},
	}
	req := workspace.ToolRequest{Tool: "submit_plan"}
	summary, errorText, prompt, reject := r.implementationPreWriteNonWriteRecovery(req, nil)

	if reject {
		t.Fatalf("plan-execute planner phase submit_plan must NOT be rejected by pre-write gate; got summary=%q error=%q prompt=%q", summary, errorText, prompt)
	}

	// Plan-execute planner phases no longer use the generic pre-write
	// write-only gate. Reads are governed by the plan-submission
	// readiness contract, not by implementation-lane write recovery.
	reqRead := workspace.ToolRequest{Tool: "read_file"}
	_, _, _, rejectRead := r.implementationPreWriteNonWriteRecovery(reqRead, nil)

	if rejectRead {
		t.Fatal("plan-execute planner reads should not be rejected by the generic implementation pre-write gate")
	}
}

func TestPlanExecuteExactAnchorPlannerDoesNotPreHideReadTools(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "target.txt", "actual\n"); err != nil {
		t.Fatalf("write target: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"target.txt","named_gap":"confirm exact anchor before submit_plan","why_not_answering":"need current target.txt contents to assemble the plan entry"}`,
		`{"tool":"submit_plan","rationale":"Update target.txt using the confirmed exact anchor.","edits":[{"path":"target.txt","old_text":"actual\n","new_text":"updated\n","purpose":"replace the confirmed target text"}]}`,
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := "Plan submitted."
		if requests < len(replies) {
			reply = replies[requests]
		}

		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
		})
	}))

	defer server.Close()

	var (
		readSucceeded bool
		hiddenRead    bool
		readErrors    []string
	)

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 4},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			Constraints:       []string{"exact_anchor_implementation"},
			RequiredEditPaths: []string{"target.txt"},
			RequiredEditSnippets: []workerruntime.RequiredEditSnippet{
				{Path: "target.txt", Content: "stale\n"},
			},
			TargetRefs: []string{"target.txt"},
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "plan exact-anchor target edit"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Result.Tool != "read_file" {
				return
			}

			if strings.Contains(event.Result.Error, "hidden for the rest of this turn") {
				hiddenRead = true
			}

			if strings.TrimSpace(event.Result.Error) != "" {
				readErrors = append(readErrors, event.Result.Error)
				return
			}

			if strings.Contains(event.Result.Output, "actual") {
				readSucceeded = true
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if hiddenRead {
		t.Fatalf("plan-execute exact-anchor planner read_file was hidden before submit_plan; read errors=%v answer=%q", readErrors, answer)
	}

	if !readSucceeded {
		t.Fatalf("expected plan-execute exact-anchor planner to read target.txt before submit_plan; read errors=%v answer=%q", readErrors, answer)
	}

	if strings.Contains(answer, "Exact-anchor implementation did not complete because") {
		t.Fatalf("plan-execute planner should not return exact-anchor hidden-tool blocker, got %q", answer)
	}

	if requests < len(replies) {
		t.Fatalf("expected read then submit_plan, got %d request(s)", requests)
	}
}

func TestPlanExecutePlannerRejectsRepeatedReadBeyondKnownEOF(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "target.txt", "one\ntwo\nthree\n"); err != nil {
		t.Fatalf("write target: %v", err)
	}

	replies := []string{
		`{"tool":"read_file","path":"target.txt","start_line":20,"end_line":30,"named_gap":"inspect tail before planning","why_not_answering":"need current source before submit_plan"}`,
		`{"tool":"read_file","path":"target.txt","start_line":30,"end_line":40,"named_gap":"keep reading","why_not_answering":"need more source"}`,
		`{"tool":"submit_plan","rationale":"Use the already gathered target.txt evidence instead of probing beyond EOF.","edits":[{"path":"target.txt","old_text":"one\n","new_text":"uno\n","purpose":"update first line"}]}`,
	}
	requests := 0

	var requestBodies []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}

		requestBodies = append(requestBodies, string(body))

		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}

		reply := replies[requests]
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
		})
	}))

	defer server.Close()

	var clampedReadSeen bool

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		Policy: RunPolicy{MaxToolSteps: 5, MaxNoProgressTurns: 5},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"target.txt"},
			TargetRefs:        []string{"target.txt"},
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "plan target edit"}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Request.Tool != "read_file" || event.Request.Path != "target.txt" {
				return
			}

			if strings.Contains(event.Result.Summary, "was clamped to file bounds") && strings.TrimSpace(event.Result.Error) == "" {
				clampedReadSeen = true
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !clampedReadSeen {
		t.Fatalf("expected first out-of-range read to clamp once, answer=%q", answer)
	}

	if !strings.Contains(answer, "Plan submitted") {
		t.Fatalf("expected planner to recover by submitting the plan, got %q", answer)
	}

	if requests != len(replies) {
		t.Fatalf("expected read, rejected retry, then submit_plan, got %d request(s)", requests)
	}

	if len(requestBodies) < 3 ||
		(!strings.Contains(requestBodies[2], "# Terminal Plan-Execute Textual Tool Surface") &&
			!strings.Contains(requestBodies[2], "# Terminal Plan-Execute Native Tool Surface")) {

		t.Fatalf("out-of-range read rejection should switch the next planner request to terminal submit-plan surface; request=%s", requestBodies[min(len(requestBodies)-1, 2)])
	}

	if strings.Contains(requestBodies[2], `"name":"read_file"`) || !strings.Contains(requestBodies[2], `"name":"submit_plan"`) {
		t.Fatalf("out-of-range read terminal request should expose only submit_plan as a callable tool; request=%s", requestBodies[2])
	}
}

func TestPlanExecuteExactAnchorPlannerDoesNotUseImplementationPreWriteSurface(t *testing.T) {
	covered := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		Constraints:       []string{"exact_anchor_implementation"},
		RequiredEditPaths: []string{"a.txt", "b.txt"},
		RequiredEditSnippets: []workerruntime.RequiredEditSnippet{
			{Path: "a.txt", Content: "old a"},
			{Path: "b.txt", Content: "old b"},
		},
	}
	if exactAnchorPreWriteSurfaceActive(covered, nil) {
		t.Fatal("plan-execute planner must not use the implementation exact-anchor pre-write surface")
	}

	missing := covered

	missing.RequiredEditSnippets = []workerruntime.RequiredEditSnippet{{Path: "a.txt", Content: "old a"}}

	if exactAnchorPreWriteSurfaceActive(missing, nil) {
		t.Fatal("plan-execute planner must keep using the plan-execute recovery surface even with partial snippets")
	}
}

func TestRepeatedCompletionGateRejectionsReturnTypedBlocker(t *testing.T) {
	replies := []string{"Done.", "Done.", "Done."}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}

		reply := replies[requests]
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))

	defer server.Close()

	var completion runnerCompletionStatus

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Policy: RunPolicy{MaxToolSteps: 10, MaxNoProgressTurns: 10},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			RequiredEditPaths:    []string{"foo.go"},
			VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
			Constraints:          []string{"planner_owned_verification"},
		},
		SeedResults: []workspace.ToolResult{
			// Source-file write requires source-aware sanity; gate
			// rejects "Done." and forces a typed blocker. Non-source
			// writes (.md/.txt) are exempt by design — see
			// hasPendingVerificationAfterWrites.
			{Tool: "edit_file", Summary: "Edited foo.go"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role != "completion_status" {
				return
			}

			if err := json.Unmarshal([]byte(event.Content), &completion); err != nil {
				t.Fatalf("decode completion status: %v", err)
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Update foo.go."}},
		llama.ChatOptions{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if requests != 2 {
		t.Fatalf("expected deterministic stop on second mutation gate rejection, got %d request(s)", requests)
	}

	for _, want := range []string{"<<QUESTION>>:", "Completion gate rejected", "foo.go", "lane sanity"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected blocker answer to contain %q, got:\n%s", want, answer)
		}
	}

	if completion.Status != "blocked" || completion.BlockerKind != "completion_gate_lane_sanity_repeated" || !completion.UserVisibleBlocker {
		t.Fatalf("unexpected completion status: %#v", completion)
	}

	if !completion.EditAttempted || !slices.Contains(completion.ChangedPaths, "foo.go") {
		t.Fatalf("expected completion status to carry edited path, got %#v", completion)
	}
}

func TestCompletionGateAttachesVerificationRecoveryAfterPreWriteRejectionAndWrite(t *testing.T) {
	root := t.TempDir()
	if err := writeTestFile(root, "go.mod", "module example.com/target\n\ngo 1.24\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	if err := writeTestFile(root, "target.go", "package target\n\nfunc Name() string { return \"old\" }\n"); err != nil {
		t.Fatalf("write target: %v", err)
	}

	replies := []string{
		"Done.",
		`{"tool":"edit_file","path":"target.go","old_text":"return \"old\"","new_text":"return \"new\"","description":"update displayed name"}`,
		"Done.",
		`{"tool":"shell","command":"go test ./..."}`,
		"Updated target.go and go test ./... passed.",
	}

	type requestPayload struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Tools []llama.Tool `json:"tools"`
	}

	requests := 0

	var (
		postWriteRequest requestPayload
		recoveryRequest  requestPayload
		recoveryPrompt   string
		postWritePrompt  string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}

		if requests == 3 {
			if err := json.Unmarshal(body, &recoveryRequest); err != nil {
				t.Fatalf("decode verification recovery request: %v\n%s", err, string(body))
			}
		}

		if requests == 2 {
			if err := json.Unmarshal(body, &postWriteRequest); err != nil {
				t.Fatalf("decode post-write request: %v\n%s", err, string(body))
			}
		}

		reply := replies[requests]
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))

	defer server.Close()

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			Shell:        "/bin/sh",
			EnableWrites: true,
			EnableShell:  true,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 8},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"target.go"},
		},
		OnEvent: func(event RunEvent) {
			if event.Role == "post_write_verification_required" {
				postWritePrompt = event.Content
			}

			if event.Role == "completion_gate_recovery" && strings.Contains(event.Content, "post-write verification") {
				recoveryPrompt = event.Content
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Update target.go."}},
		llama.ChatOptions{MaxTokens: 8192},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if requests != len(replies) {
		t.Fatalf("expected verification recovery after write, got %d request(s)", requests)
	}

	for _, want := range []string{
		"post-write verification",
		"parser-readable verification action",
		"language-appropriate verification",
		"target.go",
	} {
		if !strings.Contains(recoveryPrompt, want) {
			t.Fatalf("expected recovery prompt to contain %q, got:\n%s", want, recoveryPrompt)
		}

		if !strings.Contains(postWritePrompt, want) {
			t.Fatalf("expected post-write prompt to contain %q, got:\n%s", want, postWritePrompt)
		}
	}

	var postWriteText strings.Builder

	for _, message := range postWriteRequest.Messages {
		postWriteText.WriteString(message.Role)
		postWriteText.WriteString(": ")
		postWriteText.WriteString(message.Content)
		postWriteText.WriteString("\n")
	}

	if !strings.Contains(postWriteText.String(), postWritePrompt) {
		t.Fatalf("post-write verification prompt was not attached before the premature final answer:\n%s", postWriteText.String())
	}

	postWriteTools := map[string]bool{}
	for _, tool := range postWriteRequest.Tools {
		postWriteTools[strings.TrimSpace(tool.Function.Name)] = true
	}

	if !postWriteTools["shell"] {
		t.Fatalf("post-write verification request must expose shell, got tools=%v", postWriteTools)
	}

	var recoveryText strings.Builder

	for _, message := range recoveryRequest.Messages {
		recoveryText.WriteString(message.Role)
		recoveryText.WriteString(": ")
		recoveryText.WriteString(message.Content)
		recoveryText.WriteString("\n")
	}

	if !strings.Contains(recoveryText.String(), recoveryPrompt) {
		t.Fatalf("verification recovery prompt was not attached to the next model request:\n%s", recoveryText.String())
	}

	if !strings.Contains(answer, "go test ./... passed") {
		t.Fatalf("expected verified final answer, got:\n%s", answer)
	}
}

func TestPartialRequiredEditsCompletionGateUsesBoundedRecovery(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"a.txt", "b.txt"} {
		if err := writeTestFile(root, path, "before\n"); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	replies := []string{
		`{"tool":"write_file","path":"a.txt","content":"after a\n","description":"partial required edit"}`,
		"Done.",
		"Done again.",
	}

	var (
		requests                      int
		recoveryPrompt                string
		secondRequestMaxTokens        int
		secondRequestForcedNoThinking bool
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(replies) {
			t.Fatalf("unexpected extra chat request")
		}

		var body struct {
			MaxTokens          int `json:"max_tokens"`
			ChatTemplateKwargs *struct {
				EnableThinking *bool `json:"enable_thinking"`
			} `json:"chat_template_kwargs,omitempty"`
		}

		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}

		if requests == 2 {
			secondRequestMaxTokens = body.MaxTokens
			secondRequestForcedNoThinking = body.ChatTemplateKwargs != nil &&
				body.ChatTemplateKwargs.EnableThinking != nil &&
				!*body.ChatTemplateKwargs.EnableThinking
		}

		reply := replies[requests]
		requests++

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": reply},
			}},
		})
	}))

	defer server.Close()

	var completion runnerCompletionStatus

	runner := &Runner{
		Client: llama.NewClient(server.URL, time.Second),
		Workspace: workspace.NewManager(config.Workspace{
			Root:         root,
			CWD:          root,
			EnableWrites: true,
		}),
		Policy: RunPolicy{MaxToolSteps: 8, MaxNoProgressTurns: 6},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			VerificationContract: "not_required_for_text_mutations",
			RequiredEditPaths:    []string{"a.txt", "b.txt"},
		},
		OnEvent: func(event RunEvent) {
			switch event.Role {
			case "completion_gate_recovery":
				recoveryPrompt = event.Content
			case "completion_status":
				if err := json.Unmarshal([]byte(event.Content), &completion); err != nil {
					t.Fatalf("decode completion status: %v", err)
				}
			}
		},
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: "Update both required files."}},
		llama.ChatOptions{MaxTokens: 8192},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if requests != len(replies) {
		t.Fatalf("expected one recovery retry after partial edit, got %d request(s)", requests)
	}

	if !strings.Contains(recoveryPrompt, "b.txt") ||
		!strings.Contains(recoveryPrompt, "parser-readable action") ||
		!strings.Contains(recoveryPrompt, "Hidden-only reasoning is not completion evidence") ||
		!strings.Contains(recoveryPrompt, "edit_file") ||
		!strings.Contains(recoveryPrompt, "write_file") ||
		!strings.Contains(recoveryPrompt, "Prefer the next unread source-body request now") ||
		!strings.Contains(recoveryPrompt, "\"path\":\"b.txt\"") ||
		strings.Contains(recoveryPrompt, "Read/search/discovery tools are hidden") {

		t.Fatalf("expected recovery prompt to target remaining required path, got %q", recoveryPrompt)
	}

	if secondRequestMaxTokens != 4096 {
		t.Fatalf("expected completion-gate recovery retry to cap max_tokens at 4096, got %d", secondRequestMaxTokens)
	}

	if secondRequestForcedNoThinking {
		t.Fatalf("direct required-path recovery should not force no-thinking for local implementation retries")
	}

	if completion.BlockerKind != "completion_gate_required_paths_remaining_repeated" {
		t.Fatalf("unexpected completion status: %#v", completion)
	}

	for _, want := range []string{"<<QUESTION>>:", "a.txt", "b.txt", "Required edit paths not changed", "Path-level blockers", "b.txt: no successful write"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected blocker answer to contain %q, got:\n%s", want, answer)
		}
	}
}
