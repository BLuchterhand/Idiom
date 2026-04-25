package workerruntime

import (
	"strings"
	"testing"

	"idiom/internal/workspace"
)

func TestBuildActionGuidanceForBootstrappedReadBlocker(t *testing.T) {
	req := workspace.ToolRequest{Tool: "read_file", Path: "internal/app/app.go"}
	result := workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Implementation bootstrap already inspected this target",
		Error:   "internal/app/app.go has already been read in the implementation bootstrap; use edit_file or a narrow line range instead",
	}
	guidance := BuildActionGuidance(ActionGuidanceInput{
		Reason:                    "pre_execute_guard",
		Request:                   req,
		Result:                    result,
		DeclarationMovesForbidden: true,
	})

	if !containsString(guidance.Constraints, "declaration_moves_forbidden") {
		t.Fatalf("missing declaration move constraint: %#v", guidance)
	}
	if !containsString(guidance.Constraints, "bootstrap_evidence_available") {
		t.Fatalf("missing bootstrap evidence constraint: %#v", guidance)
	}
	if !containsString(guidance.InvalidNext, "duplicate_read_file_on_bootstrapped_target") {
		t.Fatalf("missing duplicate read invalid action: %#v", guidance)
	}
	joined := strings.Join(guidance.ValidNext, "\n")
	if !strings.Contains(joined, "edit_file on the approved target") || !strings.Contains(joined, "narrow read_file") {
		t.Fatalf("missing expected valid next actions: %#v", guidance.ValidNext)
	}
}

func TestBuildActionGuidanceIgnoresCompletionGatePhrasesInSearchOutput(t *testing.T) {
	guidance := BuildActionGuidance(ActionGuidanceInput{
		Reason:  "tool_result",
		Request: workspace.ToolRequest{Tool: "search_content", Path: "internal/agent/runner.go"},
		Result: workspace.ToolResult{
			Tool:    "search_content",
			Summary: "Found 20 matching line(s)",
			Output:  "internal/agent/runner.go:123: completion gate\ninternal/agent/runner.go:124: final answer rejected\n",
		},
	})
	if containsString(guidance.Constraints, "completion_gate_active") {
		t.Fatalf("search output should not fabricate completion-gate state: %#v", guidance)
	}
	if containsString(guidance.InvalidNext, "final_answer_without_satisfying_completion_gate") {
		t.Fatalf("search output should not block answers as if a gate fired: %#v", guidance)
	}
	if strings.Contains(strings.Join(guidance.ValidNext, "\n"), "gate") {
		t.Fatalf("search output should not inject gate-specific follow-up guidance: %#v", guidance)
	}
}

func TestBuildRecoveryPlanClassifiesFailure(t *testing.T) {
	cases := []struct {
		name   string
		input  RecoveryInput
		want   string
		needle string
	}{
		{
			name: "tool parse",
			input: RecoveryInput{
				Reason:  "tool_parse_rejected",
				Request: workspace.ToolRequest{Tool: "tool_parse"},
				Result:  workspace.ToolResult{Tool: "tool_parse", Summary: "Malformed tool request rejected", Error: "Emit exactly one valid JSON tool object."},
			},
			want:   "tool_parse_error",
			needle: "markdown-wrapped or malformed tool JSON",
		},
		{
			name: "permission",
			input: RecoveryInput{
				Reason:  "permission_denied",
				Request: workspace.ToolRequest{Tool: "edit_file", Path: "README.md"},
				Result:  workspace.ToolResult{Tool: "edit_file", Summary: "Write approval required", Error: "approval required before editing README.md"},
			},
			want:   "permission_boundary",
			needle: "stop and report that write approval is required",
		},
		{
			name: "completion gate",
			input: RecoveryInput{
				Reason:  "completion_gate_rejected",
				Request: workspace.ToolRequest{Tool: "completion_gate"},
				Result:  workspace.ToolResult{Tool: "completion_gate", Summary: "Task ledger completion gate rejected final answer", Error: "completion gate still requires pending work"},
			},
			want:   "completion_gate",
			needle: "satisfy the completion gate before final answer",
		},
		{
			name: "repeated action",
			input: RecoveryInput{
				Reason:  "tool_replayed",
				Request: workspace.ToolRequest{Tool: "read_file", Path: "README.md"},
				Result:  workspace.ToolResult{Tool: "read_file", Summary: "Repeated tool request replayed prior evidence", Error: "this exact request already replayed prior evidence once"},
			},
			want:   "repeated_action",
			needle: "same read_file request on README.md",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := BuildRecoveryPlan(tc.input)
			if plan.FailureClass != tc.want {
				t.Fatalf("FailureClass = %q, want %q in %#v", plan.FailureClass, tc.want, plan)
			}
			joined := strings.Join(append(append([]string{}, plan.DoNotRepeat...), plan.RequiredNext...), "\n")
			if !strings.Contains(joined, tc.needle) {
				t.Fatalf("expected %q in recovery guidance, got %#v", tc.needle, plan)
			}
		})
	}
}

func TestRenderStepInstructionIncludesCurrentState(t *testing.T) {
	instruction := BuildStepInstruction(RunState{
		Lane:           "implement",
		Phase:          "repair",
		Step:           4,
		EvidencePaths:  []string{"internal/app/app.go", "internal/app/app.go"},
		ChangedPaths:   []string{"internal/app/rendering.go"},
		LastTool:       "read_file",
		LastResult:     ToolResultSummary{Tool: "read_file", Error: "blocked"},
		FailureClass:   "guard_rejection",
		NextActions:    []string{"edit_file inside approved target paths"},
		InvalidActions: []string{"duplicate_read_file_on_bootstrapped_target"},
	})
	rendered := RenderStepInstruction(instruction)
	for _, needle := range []string{
		"[frontier step instruction]",
		`"lane":"implement"`,
		`"phase":"repair"`,
		`"failure_class":"guard_rejection"`,
		"duplicate_read_file_on_bootstrapped_target",
		"internal/app/app.go",
	} {
		if !strings.Contains(rendered, needle) {
			t.Fatalf("expected %q in rendered instruction:\n%s", needle, rendered)
		}
	}
	if strings.Count(rendered, "internal/app/app.go") != 1 {
		t.Fatalf("expected duplicate paths to be normalized in rendered instruction:\n%s", rendered)
	}
}

func TestRenderInitialRuntimeBriefIncludesPolicyManifestAndTargets(t *testing.T) {
	rendered := RenderInitialRuntimeBrief(WorkerRuntimeBrief{
		Lane:                 "implement",
		TaskFamily:           "implementation",
		Contract:             "implementation_result",
		Objective:            "Update routing",
		PermissionProfile:    "guarded_workspace_writes",
		TargetRefs:           []string{"internal/app/prompt_routing.go"},
		AllowedTools:         []string{"read_file", "edit_file", "shell"},
		ForbiddenTools:       []string{"mutating_shell"},
		RequiredEvidence:     []string{"changed_files_or_noop_reason", "verification_result"},
		ReadinessGate:        []string{"verification_contract_resolved"},
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
		VerificationPlan:     []string{"go test ./internal/app"},
		TargetPacket:         []string{"[implementation target packet]", "primary paths: internal/app/prompt_routing.go"},
	})

	for _, needle := range []string{
		"[frontier step instruction]",
		`"objective":"Update routing"`,
		`"allowed_tools":["read_file","edit_file","shell"]`,
		`"tool":"edit_file"`,
		"internal/app/prompt_routing.go",
		"go test ./internal/app",
	} {
		if !strings.Contains(rendered, needle) {
			t.Fatalf("expected %q in initial runtime brief:\n%s", needle, rendered)
		}
	}
}

func TestRenderInitialRuntimeBriefAddsDeterministicDiscoveryExamples(t *testing.T) {
	rendered := RenderInitialRuntimeBrief(WorkerRuntimeBrief{
		Lane:              "architecture",
		TaskFamily:        "architecture",
		Contract:          "architecture_assessment",
		PermissionProfile: "read_only_workspace",
		AllowedTools:      []string{"search_content", "read_file", "discovery_query"},
	})

	for _, needle := range []string{
		`"discovery_examples"`,
		"Use one discovery_query before ad hoc list/search/read",
		"architecture_boundary",
		"impact_analysis",
		"function_tracing",
		"security_review",
	} {
		if !strings.Contains(rendered, needle) {
			t.Fatalf("expected %q in deterministic discovery guidance:\n%s", needle, rendered)
		}
	}
}

func TestRenderInitialRuntimeBriefOmitsDiscoveryExamplesWithoutToolAccess(t *testing.T) {
	rendered := RenderInitialRuntimeBrief(WorkerRuntimeBrief{
		Lane:              "main",
		TaskFamily:        "direct_question",
		Contract:          "direct_answer",
		PermissionProfile: "conversation_only",
		AllowedTools:      []string{"discovery_query"},
	})
	if strings.Contains(rendered, "discovery_examples") || strings.Contains(rendered, "purpose\":\"impact_analysis") {
		t.Fatalf("conversation-only runtime brief should not advertise discovery examples:\n%s", rendered)
	}
}

func TestNormalizeRuntimeBriefConversationOnlyClearsToolSurface(t *testing.T) {
	brief := NormalizeRuntimeBrief(WorkerRuntimeBrief{
		PermissionProfile: "conversation_only",
		AllowedTools:      []string{"task", "search_files"},
		RequiredTools:     []string{"search_files"},
		DiscoveryExamples: []string{"use discovery_query"},
	})
	if len(brief.AllowedTools) != 0 {
		t.Fatalf("conversation-only brief should not advertise tools, got %#v", brief.AllowedTools)
	}
	if len(brief.RequiredTools) != 0 {
		t.Fatalf("conversation-only brief should not require tools, got %#v", brief.RequiredTools)
	}
	if len(brief.ToolManifest) != 0 {
		t.Fatalf("conversation-only brief should not emit a tool manifest, got %#v", brief.ToolManifest)
	}
	if len(brief.DiscoveryExamples) != 0 {
		t.Fatalf("conversation-only brief should not emit discovery examples, got %#v", brief.DiscoveryExamples)
	}
}

func TestRoleSpecForChildWriteCarriesIsolatedContract(t *testing.T) {
	spec := RoleSpecFor("workbranch_write")
	if spec.Name != "child_write" || spec.PermissionProfile != "isolated_workbranch_write" {
		t.Fatalf("unexpected child write role: %#v", spec)
	}
	for _, want := range []string{"edit_file", "apply_patch", "go_move_declarations", "shell"} {
		if !containsString(spec.AllowedTools, want) {
			t.Fatalf("expected child write role to allow %q, got %#v", want, spec.AllowedTools)
		}
	}
	for _, want := range []string{"task", "git_history_mutation", "git_index_mutation", "git_push", "publish", "deploy"} {
		if !containsString(spec.ForbiddenTools, want) {
			t.Fatalf("expected child write role to forbid %q, got %#v", want, spec.ForbiddenTools)
		}
	}
	brief := RuntimeBriefForRole("child_write", "child_write:patch", "patch README")
	if brief.Lane != "child_write:patch" || brief.ToolManifest == nil {
		t.Fatalf("expected normalized child write runtime brief, got %#v", brief)
	}
}

func TestRoleSpecForChildReadOnlyOmitsNestedTaskDelegation(t *testing.T) {
	spec := RoleSpecFor("child_read_only")
	if spec.Name != "child_read_only" || spec.PermissionProfile != "read_only_workspace" {
		t.Fatalf("unexpected child read-only role: %#v", spec)
	}
	for _, forbidden := range []string{"task", "list_files"} {
		if containsString(spec.AllowedTools, forbidden) {
			t.Fatalf("child read-only role must not advertise %q, got %#v", forbidden, spec.AllowedTools)
		}
	}
	for _, want := range []string{"read_file", "search_files", "search_content", "structural_search", "dependency_graph", "history_range", "blame_range", "semgrep_scan"} {
		if !containsString(spec.AllowedTools, want) {
			t.Fatalf("expected child read-only role to allow %q, got %#v", want, spec.AllowedTools)
		}
	}
}

func TestRoleSpecForMainUsesConversationOnlyFallback(t *testing.T) {
	spec := RoleSpecFor("main")
	if spec.Name != "main" || spec.PermissionProfile != "conversation_only" {
		t.Fatalf("unexpected main role: %#v", spec)
	}
	if len(spec.AllowedTools) != 0 {
		t.Fatalf("main role should not advertise tools by default, got %#v", spec.AllowedTools)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
