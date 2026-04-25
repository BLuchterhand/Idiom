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

func TestBuildActionGuidancePrefersClosureAfterPostChildGapBlocker(t *testing.T) {
	guidance := BuildActionGuidance(ActionGuidanceInput{
		Reason:  "pre_execute_guard",
		Request: workspace.ToolRequest{Tool: "read_file", Path: "internal/agent/runner.go"},
		Result: workspace.ToolResult{
			Tool:    "read_file",
			Summary: "Post-child follow-up missing named gap",
			Error:   "the child packet already contains sufficient evidence to bias toward answering. Before any additional read/search call, include `named_gap` describing one exact unresolved claim this follow-up will verify or falsify. Use `why_not_answering` to state what would be materially wrong or missing if you answered now.",
		},
	})
	if !containsString(guidance.Constraints, "child_packet_available") {
		t.Fatalf("expected child-packet closure constraint, got %#v", guidance)
	}
	if !containsString(guidance.InvalidNext, "open-ended discovery after readiness without one exact missing claim and one exact probe") {
		t.Fatalf("expected closure invalid action, got %#v", guidance)
	}
	joined := strings.Join(guidance.ValidNext, "\n")
	for _, want := range []string{
		"answer now from the child packet evidence",
		"one exact packet-named path, symbol, discovered handle, or narrow range",
		"materially wrong or missing if you answered now",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected closure-biased valid action %q, got %#v", want, guidance.ValidNext)
		}
	}
	for _, forbidden := range []string{"search_files", "targeted read-only shell search", "update task_ledger"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("closure-biased guidance should drop generic fallback %q, got %#v", forbidden, guidance.ValidNext)
		}
	}
}

func TestBuildRecoveryPlanPreservesClosureScopeOnRepeatedAction(t *testing.T) {
	plan := BuildRecoveryPlan(RecoveryInput{
		Reason:      "tool_rejected",
		Request:     workspace.ToolRequest{Tool: "read_file", Path: "internal/agent/answer_composer.go"},
		Constraints: []string{"answer_readiness_reached"},
		Result: workspace.ToolResult{
			Tool:    "read_file",
			Summary: "Repeated tool request rejected",
			Error:   "you just retried the same post-readiness follow-up. Do not reopen broad discovery. Either answer from the current evidence, or choose one exact symbol, discovered handle, outline, or narrow line range that resolves a still-material missing claim.",
		},
	})
	joined := strings.Join(append(append([]string{}, plan.RequiredNext...), plan.ValidNext...), "\n")
	for _, want := range []string{
		"answer now from the evidence already gathered",
		"one exact symbol, discovered handle, outline, or narrow range",
		"materially wrong or missing if you answered now",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected closure-preserving recovery guidance %q, got %#v", want, plan)
		}
	}
	for _, forbidden := range []string{"search_files", "targeted read-only shell search"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("repeated-action recovery should not reopen broad fallback %q, got %#v", forbidden, plan)
		}
	}
}

func TestBuildRecoveryPlanDoesNotPromoteEveryValidNextToRequiredNext(t *testing.T) {
	plan := BuildRecoveryPlan(RecoveryInput{
		Reason:      "tool_rejected",
		Request:     workspace.ToolRequest{Tool: "read_file", Path: "internal/agent/answer_composer.go"},
		Constraints: []string{"answer_readiness_reached"},
		Result: workspace.ToolResult{
			Tool:    "read_file",
			Summary: "Repeated tool request rejected",
			Error:   "you just retried the same post-readiness follow-up. Do not reopen broad discovery. Either answer from the current evidence, or choose one exact symbol, discovered handle, outline, or narrow line range that resolves a still-material missing claim.",
		},
	})
	if len(plan.RequiredNext) != 0 {
		t.Fatalf("expected repeated-action recovery to keep closure options in valid_next only, got %#v", plan.RequiredNext)
	}
	if len(plan.ValidNext) == 0 {
		t.Fatalf("expected repeated-action recovery to preserve valid_next options, got %#v", plan)
	}
}

func TestBuildRecoveryPlanDoesNotTreatOptionListVerificationAsRequirement(t *testing.T) {
	plan := BuildRecoveryPlan(RecoveryInput{
		Reason:       "tool_rejected",
		Request:      workspace.ToolRequest{Tool: "read_file", Path: "internal/agent/runner_child_tasks.go"},
		Constraints:  []string{"child_packet_available"},
		WriteCapable: false,
		Result: workspace.ToolResult{
			Tool:    "read_file",
			Summary: "Post-child read budget exhausted",
			Error:   "the child evidence packet has already been gathered. Synthesize the answer, make the next write, run verification, or state the concrete blocker.",
		},
	})
	if plan.FailureClass == "verification_unresolved" {
		t.Fatalf("option-list wording should not become verification_unresolved, got %#v", plan)
	}
	if containsString(plan.RequiredNext, "resolve verification status before claiming completion") {
		t.Fatalf("option-list wording should not add verification required_next, got %#v", plan)
	}
}

func TestBuildActionGuidanceClosureOmitsTextSearchFallbackAfterReadSuccess(t *testing.T) {
	guidance := BuildActionGuidance(ActionGuidanceInput{
		Reason:      "tool_result",
		Request:     workspace.ToolRequest{Tool: "read_file", Path: "internal/agent/runner_child_tasks.go"},
		Constraints: []string{"child_packet_available"},
		Result: workspace.ToolResult{
			Tool:    "read_file",
			Summary: "Read 80 line(s) from internal/agent/runner_child_tasks.go",
			Output:  "func runChildTask(...) {}\n",
		},
	})
	joined := strings.Join(guidance.ValidNext, "\n")
	for _, forbidden := range []string{
		"edit_file using the inspected evidence",
		"exact text search",
		"answer if the inspected body already resolves the question",
	} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("closure guidance should omit %q, got %#v", forbidden, guidance.ValidNext)
		}
	}
	for _, want := range []string{"answer now from the child packet evidence", "one exact packet-named path, symbol, discovered handle, or narrow range"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected closure guidance to keep %q, got %#v", want, guidance.ValidNext)
		}
	}
}

func TestBuildActionGuidanceClosureOmitsOutlineFollowupMenu(t *testing.T) {
	guidance := BuildActionGuidance(ActionGuidanceInput{
		Reason:      "tool_result",
		Request:     workspace.ToolRequest{Tool: "go_file_outline", Path: "internal/agent/runner.go"},
		Constraints: []string{"child_packet_available"},
		Objective:   "Explain how this harness uses subagents.",
		Result: workspace.ToolResult{
			Tool:    "go_file_outline",
			Summary: "Outlined 12 top-level declaration(s) in internal/agent/runner.go.",
			Output:  "package agent\ndeclarations:\n- runChildTask lines 640-700",
		},
	})
	joined := strings.Join(guidance.ValidNext, "\n")
	for _, forbidden := range []string{
		"go_read_symbol with one of the outlined declaration names",
		"go_references or go_calls_into on a named declaration",
		"go_move_declarations",
	} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("closure guidance should omit %q, got %#v", forbidden, guidance.ValidNext)
		}
	}
	for _, want := range []string{
		"answer now from the child packet evidence",
		"one exact packet-named path, symbol, discovered handle, or narrow range",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected closure guidance to keep %q, got %#v", want, guidance.ValidNext)
		}
	}
}

func TestBuildActionGuidanceReadSuccessOmitsEditSuggestionForExplanationObjective(t *testing.T) {
	guidance := BuildActionGuidance(ActionGuidanceInput{
		Reason:    "tool_result",
		Request:   workspace.ToolRequest{Tool: "go_read_symbol", Path: "internal/agent/runner_child_tasks.go", Pattern: "Runner.runChildTask"},
		Objective: "Explain how this harness utilizes sub-agents.",
		Result: workspace.ToolResult{
			Tool:    "go_read_symbol",
			Summary: "Read Runner.runChildTask in internal/agent/runner_child_tasks.go lines 16-131",
			Output:  "func (r *Runner) runChildTask(...) {}",
		},
	})
	joined := strings.Join(guidance.ValidNext, "\n")
	if strings.Contains(joined, "edit_file using the inspected evidence") {
		t.Fatalf("explanatory read guidance should not suggest edits, got %#v", guidance.ValidNext)
	}
}

func TestBuildActionGuidanceSearchSuccessOmitsEditSuggestionWhenReadOnly(t *testing.T) {
	guidance := BuildActionGuidance(ActionGuidanceInput{
		Reason:  "tool_result",
		Request: workspace.ToolRequest{Tool: "search_files", Path: "internal", Pattern: "*.go"},
		Result: workspace.ToolResult{
			Tool:    "search_files",
			Summary: "Found 157 matching file(s).",
			Output:  "internal/agent/runner.go\ninternal/agent/runner_child_tasks.go\n",
		},
	})
	joined := strings.Join(guidance.ValidNext, "\n")
	if strings.Contains(joined, "edit_file if the target is already clear") {
		t.Fatalf("read-only search guidance should not suggest edits, got %#v", guidance.ValidNext)
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

func TestBuildStepInstructionUsesClosureDirective(t *testing.T) {
	instruction := BuildStepInstruction(RunState{
		Lane:        "main",
		Phase:       "evidence",
		Step:        3,
		Constraints: []string{"child_packet_available"},
		NextActions: []string{"answer directly from the child packet if it already resolves the question"},
		InvalidActions: []string{
			"open-ended discovery after a child packet without naming a concrete gap",
		},
	})
	if !strings.Contains(instruction.Directive, "Prefer answering now") {
		t.Fatalf("expected closure-biased directive, got %q", instruction.Directive)
	}
	if !strings.Contains(instruction.Directive, "one exact missing claim") || !strings.Contains(instruction.Directive, "one exact probe") {
		t.Fatalf("expected exact-claim/exact-probe language, got %q", instruction.Directive)
	}
	if !strings.Contains(instruction.Directive, "ranked options, not mandatory commands") {
		t.Fatalf("expected closure directive to clarify option-vs-command, got %q", instruction.Directive)
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

func TestRenderFailureFeedbackPacketExplainsRequiredVsValid(t *testing.T) {
	rendered := RenderFailureFeedbackPacket(map[string]any{
		"required_next": []string{"emit exactly one valid JSON tool object"},
		"valid_next":    []string{"answer from the current evidence if it already resolves the task"},
	})
	if !strings.Contains(rendered, "`required_next` lists hard requirements; `valid_next` lists safe options") {
		t.Fatalf("expected failure-feedback directive to explain packet semantics, got %q", rendered)
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
	for _, want := range []string{"answer_when_evidence_is_sufficient", "if_not_answering_name_one_exact_missing_claim_and_probe", "prefer_symbol_or_handle_probes_over_raw_path_reads"} {
		if !containsString(spec.SearchPolicy, want) {
			t.Fatalf("expected child read-only search policy to include %q, got %#v", want, spec.SearchPolicy)
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
