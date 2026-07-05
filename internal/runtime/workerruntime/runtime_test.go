package workerruntime

import (
	"slices"
	"strings"
	"testing"

	"idiom/internal/capabilities/workspace"
	contractcompile "idiom/internal/contracts/compile"
	recoverycontract "idiom/internal/contracts/recovery"
)

func TestBuildActionGuidanceForBootstrappedReadBlocker(t *testing.T) {
	req := workspace.ToolRequest{Tool: "read_file", Path: "internal/surfaces/tui/app/app.go"}
	result := workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Implementation bootstrap already inspected this target",
		Error:   "internal/surfaces/tui/app/app.go has already been read in the implementation bootstrap; approved-target write capability or a narrow line range remains available",
	}
	guidance := BuildActionGuidance(ActionGuidanceInput{
		Reason:                    "pre_execute_guard",
		Request:                   req,
		Result:                    result,
		DeclarationMovesForbidden: true,
	})

	if !slices.Contains(guidance.Constraints, "declaration_moves_forbidden") {
		t.Fatalf("missing declaration move constraint: %#v", guidance)
	}

	if !slices.Contains(guidance.Constraints, "bootstrap_evidence_available") {
		t.Fatalf("missing bootstrap evidence constraint: %#v", guidance)
	}

	if !slices.Contains(guidance.InvalidNext, "duplicate_read_file_on_bootstrapped_target") {
		t.Fatalf("missing duplicate read invalid action: %#v", guidance)
	}

	joined := strings.Join(guidance.ValidNext, "\n")
	if !strings.Contains(joined, "approved-target write capability") || !strings.Contains(joined, "narrow range read") {
		t.Fatalf("missing expected valid next actions: %#v", guidance.ValidNext)
	}
}

func TestRenderActiveProtocolCardUsesCompiledProjection(t *testing.T) {
	rendered := RenderActiveProtocolCard(contractcompile.ModelProtocolProjection{
		ContractID:              "contract-1",
		ProtocolHash:            "hash-123",
		PhaseID:                 "required_source",
		VisibleTools:            []string{"submit_plan", "read_file", "read_file", "code_navigate"},
		CallableTools:           []string{"submit_plan", "code_navigate"},
		TerminalResponseShapes:  []string{"submit_plan", "<<QUESTION>>:", "Decision: source-satisfied"},
		InvalidActionClasses:    []string{"submit_plan", "generic_probe"},
		RequiredEditCoverage:    []string{"internal/a.go"},
		SourceBodyObligations:   []string{"internal/a.go"},
		PathRoleSummary:         []string{"required_edit_path: internal/a.go"},
		VerificationObligations: []string{"go test ./..."},
		SafeAlternatives:        []string{"generic_probe", "<<QUESTION>>:"},
	})

	for _, want := range []string{
		"[active contract protocol]",
		"Active contract: contract-1 / protocol_hash=hash-123",
		"Phase: required_source",
		"Visible tools: [submit_plan, code_navigate, read_file]",
		"Callable tools now: [submit_plan, code_navigate]",
		"Valid terminal outcomes: Decision: source-satisfied; <<QUESTION>>:",
		"Invalid now: submit_plan, generic_probe",
		"Path roles: required_edit_paths=[internal/a.go]; source_body_obligations=[internal/a.go]; roles=[required_edit_path: internal/a.go]",
		"Verification: go test ./...",
		"Safe alternatives: <<QUESTION>>:; generic_probe",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("missing %q in rendered protocol card:\n%s", want, rendered)
		}
	}

	if strings.Contains(rendered, "submit_plan; Decision: source-satisfied") {
		t.Fatalf("terminal outcomes should suppress submit_plan when submit_plan is invalid:\n%s", rendered)
	}
}

func TestRenderActiveProtocolCardOmitsEmptyProjection(t *testing.T) {
	if rendered := RenderActiveProtocolCard(contractcompile.ModelProtocolProjection{ContractID: "contract-1"}); rendered != "" {
		t.Fatalf("empty protocol hash should not render card, got %q", rendered)
	}
}

func TestIsZeroHitSearchResultDetectsRoutedSymbolMiss(t *testing.T) {
	result := workspace.ToolResult{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output:  "capability: read_symbol\nlanguage: go\nrouted_tool: go_read_symbol\nroute_summary: No Go symbols matched \"CoverageGate\"",
	}

	if !IsZeroHitSearchResult(result) {
		t.Fatalf("routed symbol miss should count as a zero-hit result")
	}
}

func TestIsZeroHitSearchResultDetectsContentSearchMiss(t *testing.T) {
	result := workspace.ToolResult{
		Tool:    "search_content",
		Summary: `No content matches for "pkey.KeyArrowUp"`,
	}
	if !IsZeroHitSearchResult(result) {
		t.Fatal("expected No content matches summary to count as zero-hit search result")
	}
}

func TestIsZeroHitSearchResultIgnoresBodyText(t *testing.T) {
	result := workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read docs/search-notes.md lines 1-8",
		Output:  "1:The UI should display the phrase \"no matches\" when search is empty.\n2:Found 0 should be documented as copy, not a tool result.",
	}

	if IsZeroHitSearchResult(result) {
		t.Fatalf("source body text should not be classified as zero-hit metadata")
	}
}

func TestBuildActionGuidanceForExactAnchorWriteFailure(t *testing.T) {
	req := workspace.ToolRequest{Tool: "edit_file", Path: "tests/e2e/suite.gd"}
	result := workspace.ToolResult{
		Tool:    "edit_file",
		Summary: "Edit request rejected",
		Error:   "old_text did not appear in tests/e2e/suite.gd",
	}
	guidance := BuildActionGuidance(ActionGuidanceInput{
		Reason:       "tool_result",
		Request:      req,
		Result:       result,
		Constraints:  []string{"exact_anchor_implementation"},
		WriteCapable: true,
	})

	if !slices.Contains(guidance.InvalidNext, "read/search before resolving exact-anchor write failure") {
		t.Fatalf("missing exact-anchor invalid action: %#v", guidance)
	}

	joined := strings.Join(guidance.ValidNext, "\n")
	for _, want := range []string{
		"retry one corrected write shape from the target packet",
		"<<QUESTION>> naming the missing anchor",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected %q in exact-anchor guidance, got %#v", want, guidance.ValidNext)
		}
	}

	plan := BuildRecoveryPlan(RecoveryInput{
		Reason:       "tool_result",
		Request:      req,
		Result:       result,
		Constraints:  []string{"exact_anchor_implementation"},
		WriteCapable: true,
	})
	if plan.FailureClass != "exact_anchor_write_failure" {
		t.Fatalf("FailureClass = %q, want exact_anchor_write_failure in %#v", plan.FailureClass, plan)
	}

	recovery := strings.Join(append(append([]string{}, plan.DoNotRepeat...), plan.RequiredNext...), "\n")
	for _, want := range []string{
		"read/search after an exact-anchor write failed",
		"<<QUESTION>> naming the missing anchor",
	} {
		if !strings.Contains(recovery, want) {
			t.Fatalf("expected %q in exact-anchor recovery, got %#v", want, plan)
		}
	}
}

func TestBuildActionGuidanceForExactAnchorHiddenPrewriteTool(t *testing.T) {
	req := workspace.ToolRequest{Tool: "read_file", Path: ".planning/campaigns/megaflora-system.md"}
	result := workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Tool hidden by active contract",
		Error:   "read_file is hidden for the rest of this turn by the active runtime contract.",
	}
	guidance := BuildActionGuidance(ActionGuidanceInput{
		Reason:       "tool_rejected",
		Request:      req,
		Result:       result,
		Constraints:  []string{"exact_anchor_implementation"},
		WriteCapable: true,
	})

	if !slices.Contains(guidance.InvalidNext, "read/search/shell before first exact-anchor write") {
		t.Fatalf("missing exact-anchor hidden-tool invalid action: %#v", guidance)
	}

	joined := strings.Join(guidance.ValidNext, "\n")
	for _, want := range []string{
		"exact-anchor write capability with the supplied target-packet anchor",
		"<<QUESTION>> if no exact write anchor or patch is present",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected %q in exact-anchor hidden-tool guidance, got %#v", want, guidance.ValidNext)
		}
	}

	plan := BuildRecoveryPlan(RecoveryInput{
		Reason:       "tool_rejected",
		Request:      req,
		Result:       result,
		Constraints:  []string{"exact_anchor_implementation"},
		WriteCapable: true,
	})
	if plan.FailureClass != "exact_anchor_prewrite_hidden_tool" {
		t.Fatalf("FailureClass = %q, want exact_anchor_prewrite_hidden_tool in %#v", plan.FailureClass, plan)
	}

	recovery := strings.Join(append(append([]string{}, plan.DoNotRepeat...), plan.RequiredNext...), "\n")
	for _, want := range []string{
		"read/search/shell before first exact-anchor write",
		"exact-anchor write capability with the supplied anchor",
	} {
		if !strings.Contains(recovery, want) {
			t.Fatalf("expected %q in exact-anchor hidden-tool recovery, got %#v", want, plan)
		}
	}
}

func TestBuildActionGuidanceForPlanExecuteSubmitReady(t *testing.T) {
	req := workspace.ToolRequest{Tool: "read_file", Path: "cmd/idiom/product_workflow.go"}
	result := workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Plan-execute submit_plan required",
		Error:   "read_file is not available because the plan-execute planner is source-ready and exposes only submit_plan",
	}
	guidance := BuildActionGuidance(ActionGuidanceInput{
		Reason:      "tool_rejected",
		Request:     req,
		Result:      result,
		Constraints: []string{"plan_then_execute_submit_plan_ready"},
	})

	if !slices.Contains(guidance.InvalidNext, "read/search/source-refresh after plan-execute submit_plan readiness") {
		t.Fatalf("missing submit-ready invalid action: %#v", guidance.InvalidNext)
	}

	joined := strings.Join(guidance.ValidNext, "\n")
	for _, want := range []string{
		"submit_plan covering every required_edit_paths entry with a real edit or source_satisfied_paths entry",
		"Decision: source-satisfied",
		"<<QUESTION>>",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected %q in submit-ready guidance, got %#v", want, guidance.ValidNext)
		}
	}

	if strings.Contains(joined, "one exact tool-shaped follow-up") {
		t.Fatalf("submit-ready guidance must not fall back to generic probe advice: %#v", guidance.ValidNext)
	}

	for _, tc := range []struct {
		name   string
		req    workspace.ToolRequest
		result workspace.ToolResult
	}{
		{
			name: "successful read_file",
			req:  workspace.ToolRequest{Tool: "read_file", Path: "a.go", StartLine: 10, EndLine: 30},
			result: workspace.ToolResult{
				Tool:    "read_file",
				Summary: "Read f1 a.go lines 10-30 (total lines: 40)",
			},
		},
		{
			name: "successful code_navigate",
			req:  workspace.ToolRequest{Tool: "code_navigate", Path: "a.go", Capability: "read_symbol", Pattern: "Run"},
			result: workspace.ToolResult{
				Tool:    "code_navigate",
				Summary: "Code navigation routed read_symbol via go_read_symbol",
				Output:  "route_summary: Read Run in a.go lines 10-30\nfunc Run() {}\n",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			guidance := BuildActionGuidance(ActionGuidanceInput{
				Reason:      "tool_result",
				Request:     tc.req,
				Result:      tc.result,
				Constraints: []string{"plan_then_execute_submit_plan_ready"},
			})
			joined := strings.Join(guidance.ValidNext, "\n")

			for _, want := range []string{
				"submit_plan covering every required_edit_paths entry with a real edit or source_satisfied_paths entry",
				"Decision: source-satisfied",
				"<<QUESTION>>",
			} {
				if !strings.Contains(joined, want) {
					t.Fatalf("expected %q in submit-ready guidance, got %#v", want, guidance.ValidNext)
				}
			}

			for _, forbidden := range []string{
				"one exact missing claim",
				"narrow range read",
				"exact-text search",
				"answer if the inspected body",
				"one exact tool-shaped follow-up",
			} {
				if strings.Contains(joined, forbidden) {
					t.Fatalf("submit-ready guidance leaked read/probe action %q: %#v", forbidden, guidance.ValidNext)
				}
			}
		})
	}

	plan := BuildRecoveryPlan(RecoveryInput{
		Reason:      "tool_rejected",
		Request:     req,
		Result:      result,
		Constraints: []string{"plan_then_execute_submit_plan_ready"},
	})
	joinedPlan := strings.Join(plan.ValidNext, "\n")

	if strings.Contains(joinedPlan, "one exact tool-shaped follow-up") {
		t.Fatalf("submit-ready recovery must not suggest another probe: %#v", plan.ValidNext)
	}

	if !strings.Contains(joinedPlan, "submit_plan covering every required_edit_paths entry with a real edit or source_satisfied_paths entry") {
		t.Fatalf("submit-ready recovery must keep submit_plan closeout, got %#v", plan.ValidNext)
	}

	parseGuidance := BuildActionGuidance(ActionGuidanceInput{
		Reason:      "tool_parse_rejected",
		Request:     workspace.ToolRequest{Tool: "tool_parse"},
		Result:      workspace.ToolResult{Tool: "tool_parse", Summary: "Malformed tool request rejected", Error: "decode tool request"},
		Constraints: []string{"plan_then_execute_submit_plan_ready"},
	})
	joinedParse := strings.Join(parseGuidance.ValidNext, "\n")

	if !strings.Contains(joinedParse, "parser-readable submit_plan") {
		t.Fatalf("submit-ready parse recovery should require parser-readable submit_plan, got %#v", parseGuidance.ValidNext)
	}

	if strings.Contains(joinedParse, "answer normally only if the task is complete") {
		t.Fatalf("submit-ready parse recovery must not use generic parse fallback, got %#v", parseGuidance.ValidNext)
	}

	parsePlan := BuildRecoveryPlan(RecoveryInput{
		Reason:      "tool_parse_rejected",
		Request:     workspace.ToolRequest{Tool: "tool_parse"},
		Result:      workspace.ToolResult{Tool: "tool_parse", Summary: "Malformed tool request rejected", Error: "decode tool request"},
		Constraints: []string{"plan_then_execute_submit_plan_ready"},
	})
	joinedRequired := strings.Join(parsePlan.RequiredNext, "\n")

	if !strings.Contains(joinedRequired, "parser-readable submit_plan") {
		t.Fatalf("submit-ready recovery plan should require parser-readable submit_plan, got %#v", parsePlan.RequiredNext)
	}

	if strings.Contains(joinedRequired, "valid JSON tool object") {
		t.Fatalf("submit-ready recovery plan must not use generic JSON recovery, got %#v", parsePlan.RequiredNext)
	}

	nativeParseGuidance := BuildActionGuidance(ActionGuidanceInput{
		Reason:            "tool_parse_rejected",
		Request:           workspace.ToolRequest{Tool: "tool_parse"},
		Result:            workspace.ToolResult{Tool: "tool_parse", Summary: "Malformed tool request rejected", Error: "malformed DeepSeek XML tool request"},
		Constraints:       []string{"plan_then_execute_submit_plan_ready"},
		ToolTransportMode: "native",
	})
	joinedNativeGuidance := strings.Join(nativeParseGuidance.ValidNext, "\n")
	if !strings.Contains(joinedNativeGuidance, "active native submit_plan tool call") {
		t.Fatalf("native submit-ready parse recovery should preserve native submit_plan transport, got %#v", nativeParseGuidance.ValidNext)
	}
	if strings.Contains(joinedNativeGuidance, "parser-readable submit_plan JSON object") {
		t.Fatalf("native submit-ready parse recovery must not ask for textual submit_plan JSON, got %#v", nativeParseGuidance.ValidNext)
	}

	nativeParsePlan := BuildRecoveryPlan(RecoveryInput{
		Reason:            "tool_parse_rejected",
		Request:           workspace.ToolRequest{Tool: "tool_parse"},
		Result:            workspace.ToolResult{Tool: "tool_parse", Summary: "Malformed tool request rejected", Error: "malformed DeepSeek XML tool request"},
		Constraints:       []string{"plan_then_execute_submit_plan_ready"},
		ToolTransportMode: "native",
	})
	joinedNativeRequired := strings.Join(nativeParsePlan.RequiredNext, "\n")
	if !strings.Contains(joinedNativeRequired, "active native submit_plan tool call") {
		t.Fatalf("native submit-ready recovery plan should preserve native submit_plan transport, got %#v", nativeParsePlan.RequiredNext)
	}
	if strings.Contains(joinedNativeRequired, "parser-readable submit_plan JSON object") {
		t.Fatalf("native submit-ready recovery plan must not ask for textual submit_plan JSON, got %#v", nativeParsePlan.RequiredNext)
	}
	joinedNativeValid := strings.Join(nativeParsePlan.ValidNext, "\n")
	if !strings.Contains(joinedNativeValid, "active native submit_plan tool call") {
		t.Fatalf("native submit-ready recovery plan should keep submit_plan-specific valid next action, got %#v", nativeParsePlan.ValidNext)
	}
	if strings.Contains(joinedNativeValid, "active native tool-call transport for one callable tool") {
		t.Fatalf("native submit-ready recovery plan must not use generic native tool action, got %#v", nativeParsePlan.ValidNext)
	}
}

func TestBuildStepInstructionForPlanExecuteSubmitReady(t *testing.T) {
	inst := BuildStepInstruction(RunState{
		TaskFamily:        "plan_then_execute_planner",
		Constraints:       []string{"plan_then_execute_submit_plan_ready"},
		RequiredEditPaths: []string{"a.go", "a_test.go"},
	})

	if !strings.Contains(inst.Directive, "source obligations are closed") {
		t.Fatalf("expected submit-ready directive, got %q", inst.Directive)
	}

	if strings.Contains(inst.Directive, "narrow required-path source read") {
		t.Fatalf("submit-ready directive must not advertise source reads, got %q", inst.Directive)
	}
}

func TestBuildActionGuidanceForWriteReadyImplementationPacket(t *testing.T) {
	guidance := BuildActionGuidance(ActionGuidanceInput{
		Reason:       "tool_result",
		Request:      workspace.ToolRequest{Tool: "read_file", Path: "native/tests/test_cart_system.cpp", StartLine: 100, EndLine: 160},
		Constraints:  []string{"implementation_packet_write_ready"},
		WriteCapable: true,
		Result: workspace.ToolResult{
			Tool:    "read_file",
			Summary: "Read native/tests/test_cart_system.cpp lines 100-160",
			Output:  "TEST_CASE(\"existing pattern\") {}",
		},
	})
	if !slices.Contains(guidance.InvalidNext, "orientation read/search after required edit paths and packet evidence without a named missing fact") {
		t.Fatalf("missing write-ready invalid action: %#v", guidance)
	}

	joined := strings.Join(guidance.ValidNext, "\n")
	for _, want := range []string{
		"required-path write capability",
		"required-path edit from the packet and inspected evidence",
		"do not take another orientation read",
		"<<QUESTION>> naming the exact edit-safety fact",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected %q in write-ready guidance, got %#v", want, guidance.ValidNext)
		}
	}

	if strings.Contains(joined, "one more targeted probe") {
		t.Fatalf("write-ready guidance must not suggest another probe, got %#v", guidance.ValidNext)
	}
}

func TestBuildActionGuidanceForRequiredPathGateDoesNotNudgeMoreReads(t *testing.T) {
	guidance := BuildActionGuidance(ActionGuidanceInput{
		Reason:       "completion_gate_rejected",
		Request:      workspace.ToolRequest{},
		Constraints:  []string{"implementation_packet_write_ready", "implementation_required_paths_remaining"},
		WriteCapable: true,
		Result: workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Mutation completion gate rejected final answer",
			Error:   "required edit path(s) were not changed: native/tests/test_energy.cpp.",
		},
	})
	joined := strings.Join(guidance.ValidNext, "\n")

	if strings.Contains(joined, "gather or edit the missing evidence") {
		t.Fatalf("required-path gate should not suggest gathering more evidence, got %#v", guidance.ValidNext)
	}

	for _, want := range []string{
		"satisfy the missing write contract with an allowed edit",
		"answer only after the gate condition is satisfied",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected %q in required-path gate guidance, got %#v", want, guidance.ValidNext)
		}
	}
}

func TestBuildActionGuidanceIgnoresCompletionGatePhrasesInSearchOutput(t *testing.T) {
	guidance := BuildActionGuidance(ActionGuidanceInput{
		Reason:  "tool_result",
		Request: workspace.ToolRequest{Tool: "search_content", Path: "internal/runtime/agent/runner.go"},
		Result: workspace.ToolResult{
			Tool:    "search_content",
			Summary: "Found 20 matching line(s)",
			Output:  "internal/runtime/agent/runner.go:123: completion gate\ninternal/runtime/agent/runner.go:124: final answer rejected\n",
		},
	})
	if slices.Contains(guidance.Constraints, "completion_gate_active") {
		t.Fatalf("search output should not fabricate completion-gate state: %#v", guidance)
	}

	if slices.Contains(guidance.InvalidNext, "final_answer_without_satisfying_completion_gate") {
		t.Fatalf("search output should not block answers as if a gate fired: %#v", guidance)
	}

	if strings.Contains(strings.Join(guidance.ValidNext, "\n"), "gate") {
		t.Fatalf("search output should not inject gate-specific follow-up guidance: %#v", guidance)
	}
}

func TestBuildActionGuidancePrefersClosureAfterPostChildGapBlocker(t *testing.T) {
	guidance := BuildActionGuidance(ActionGuidanceInput{
		Reason:  "pre_execute_guard",
		Request: workspace.ToolRequest{Tool: "read_file", Path: "internal/runtime/agent/runner.go"},
		Result: workspace.ToolResult{
			Tool:    "read_file",
			Summary: "Post-child follow-up missing named gap",
			Error:   "the child packet already contains sufficient evidence to bias toward answering. Before any additional read/search call, include `named_gap` describing one exact unresolved claim this follow-up will verify or falsify. Use `why_not_answering` to state what would be materially wrong or missing if you answered now.",
		},
	})
	if !slices.Contains(guidance.Constraints, "child_packet_available") {
		t.Fatalf("expected child-packet closure constraint, got %#v", guidance)
	}

	if !slices.Contains(guidance.InvalidNext, "open-ended discovery after readiness without one exact missing claim and one exact probe") {
		t.Fatalf("expected closure invalid action, got %#v", guidance)
	}

	joined := strings.Join(guidance.ValidNext, "\n")
	for _, want := range []string{
		"final answer from the child packet evidence is available",
		"one exact named path, symbol, discovered handle, or narrow range",
		"<<QUESTION>> with the exact blocker",
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
		Request:     workspace.ToolRequest{Tool: "read_file", Path: "internal/runtime/agent/answer_composer.go"},
		Constraints: []string{"answer_readiness_reached"},
		Result: workspace.ToolResult{
			Tool:    "read_file",
			Summary: "Repeated tool request rejected",
			Error:   "you just retried the same post-readiness follow-up. Do not reopen broad discovery. Either answer from the current evidence, or choose one exact symbol, discovered handle, or narrow line range that resolves a still-material missing claim.",
		},
	})
	joined := strings.Join(append(append([]string{}, plan.RequiredNext...), plan.ValidNext...), "\n")

	for _, want := range []string{
		"final answer from the evidence already gathered is available",
		"one exact symbol, discovered handle, or narrow range",
		"<<QUESTION>> with the exact blocker",
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
		Request:     workspace.ToolRequest{Tool: "read_file", Path: "internal/runtime/agent/answer_composer.go"},
		Constraints: []string{"answer_readiness_reached"},
		Result: workspace.ToolResult{
			Tool:    "read_file",
			Summary: "Repeated tool request rejected",
			Error:   "you just retried the same post-readiness follow-up. Do not reopen broad discovery. Either answer from the current evidence, or choose one exact symbol, discovered handle, or narrow line range that resolves a still-material missing claim.",
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
		Request:      workspace.ToolRequest{Tool: "read_file", Path: "internal/runtime/agent/runner_child_tasks.go"},
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

	if slices.Contains(plan.RequiredNext, "resolve verification status before claiming completion") {
		t.Fatalf("option-list wording should not add verification required_next, got %#v", plan)
	}
}

func TestBuildRecoveryPlanTreatsFailedSourceSanityAsRepairObligation(t *testing.T) {
	plan := BuildRecoveryPlan(RecoveryInput{
		Reason:       "tool_result",
		Request:      workspace.ToolRequest{Tool: "source_sanity", Path: "internal/workflows/productworkflow/coverage_gate.go"},
		Constraints:  []string{"required_edit_paths_define_write_scope"},
		WriteCapable: true,
		Result: workspace.ToolResult{
			Tool:    "source_sanity",
			Summary: "Source sanity failed for 1 of 1 file(s)",
			Error:   "internal/workflows/productworkflow/coverage_gate.go:242: req.Must undefined",
		},
	})
	if plan.FailureClass != "verification_failed" {
		t.Fatalf("failure class = %q, want verification_failed; plan=%#v", plan.FailureClass, plan)
	}

	joinedRequired := strings.Join(plan.RequiredNext, "\n")
	if !strings.Contains(joinedRequired, "repair the failed verification") {
		t.Fatalf("expected repair required_next, got %#v", plan.RequiredNext)
	}

	joinedValid := strings.Join(plan.ValidNext, "\n")
	for _, want := range []string{
		"repair the failing changed or required path",
		"one narrow source read or language/context lookup",
		"rerun verification after the repair write",
		"answer with <<QUESTION>> only for an external",
	} {
		if !strings.Contains(joinedValid, want) {
			t.Fatalf("expected valid_next to contain %q, got %#v", want, plan.ValidNext)
		}
	}

	if strings.Contains(joinedValid, "answer from the current evidence if it already resolves the task") {
		t.Fatalf("failed verification should not fall back to generic answer-ready guidance, got %#v", plan.ValidNext)
	}
}

func TestBuildActionGuidanceClosureOmitsTextSearchFallbackAfterReadSuccess(t *testing.T) {
	guidance := BuildActionGuidance(ActionGuidanceInput{
		Reason:      "tool_result",
		Request:     workspace.ToolRequest{Tool: "read_file", Path: "internal/runtime/agent/runner_child_tasks.go"},
		Constraints: []string{"child_packet_available"},
		Result: workspace.ToolResult{
			Tool:    "read_file",
			Summary: "Read 80 line(s) from internal/runtime/agent/runner_child_tasks.go",
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

	for _, want := range []string{"final answer from the child packet evidence is available", "one exact named path, symbol, discovered handle, or narrow range"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected closure guidance to keep %q, got %#v", want, guidance.ValidNext)
		}
	}
}

func TestBuildActionGuidanceClosureOmitsOutlineFollowupMenu(t *testing.T) {
	guidance := BuildActionGuidance(ActionGuidanceInput{
		Reason:         "tool_result",
		Request:        workspace.ToolRequest{Tool: "go_file_outline", Path: "internal/runtime/agent/runner.go"},
		Constraints:    []string{"child_packet_available"},
		EvidenceIntent: "relation_trace",
		Result: workspace.ToolResult{
			Tool:    "go_file_outline",
			Summary: "Outlined 12 top-level declaration(s) in internal/runtime/agent/runner.go.",
			Output:  "package agent\ndeclarations:\n- runChildTask lines 640-700",
		},
	})
	joined := strings.Join(guidance.ValidNext, "\n")

	for _, forbidden := range []string{
		"read-symbol capability with one outlined declaration name",
		"references or calls-into capability on a named declaration",
		"go_move_declarations",
	} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("closure guidance should omit %q, got %#v", forbidden, guidance.ValidNext)
		}
	}

	for _, want := range []string{
		"final answer from the child packet evidence is available",
		"one exact named path, symbol, discovered handle, or narrow range",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected closure guidance to keep %q, got %#v", want, guidance.ValidNext)
		}
	}
}

func TestBuildActionGuidanceReadSuccessOmitsEditSuggestionForExplanationObjective(t *testing.T) {
	guidance := BuildActionGuidance(ActionGuidanceInput{
		Reason:         "tool_result",
		Request:        workspace.ToolRequest{Tool: "go_read_symbol", Path: "internal/runtime/agent/runner_child_tasks.go", Pattern: "Runner.runChildTask"},
		EvidenceIntent: "generic",
		Result: workspace.ToolResult{
			Tool:    "go_read_symbol",
			Summary: "Read Runner.runChildTask in internal/runtime/agent/runner_child_tasks.go lines 16-131",
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
			Output:  "internal/runtime/agent/runner.go\ninternal/runtime/agent/runner_child_tasks.go\n",
		},
	})
	joined := strings.Join(guidance.ValidNext, "\n")

	if strings.Contains(joined, "edit_file if the target is already clear") {
		t.Fatalf("read-only search guidance should not suggest edits, got %#v", guidance.ValidNext)
	}
}

func TestBuildRecoveryPlanClassifiesFailure(t *testing.T) {
	nativeParseGuidance := BuildActionGuidance(ActionGuidanceInput{
		Reason:            "tool_parse_rejected",
		Request:           workspace.ToolRequest{Tool: "tool_parse"},
		Result:            workspace.ToolResult{Tool: "tool_parse", Summary: "Malformed tool request rejected", Error: "malformed DeepSeek XML tool request"},
		ToolTransportMode: "native",
	})
	joinedNativeGuidance := strings.Join(nativeParseGuidance.ValidNext, "\n")
	if !strings.Contains(joinedNativeGuidance, "active native tool-call transport") {
		t.Fatalf("native action guidance should preserve native tool transport, got %#v", nativeParseGuidance.ValidNext)
	}
	if strings.Contains(joinedNativeGuidance, "valid JSON tool object") {
		t.Fatalf("native action guidance should not ask for textual JSON, got %#v", nativeParseGuidance.ValidNext)
	}

	cases := []struct {
		name            string
		input           RecoveryInput
		want            recoverycontract.FailureClass
		needle          string
		wantNoValidJSON bool
	}{
		{
			name: "tool parse",
			input: RecoveryInput{
				Reason:  "tool_parse_rejected",
				Request: workspace.ToolRequest{Tool: "tool_parse"},
				Result:  workspace.ToolResult{Tool: "tool_parse", Summary: "Malformed tool request rejected", Error: "Emit exactly one valid JSON tool object."},
			},
			want:   recoverycontract.FailureClassToolParseError,
			needle: "markdown-wrapped or malformed tool JSON",
		},
		{
			name: "tool parse native transport",
			input: RecoveryInput{
				Reason:            "tool_parse_rejected",
				Request:           workspace.ToolRequest{Tool: "tool_parse"},
				Result:            workspace.ToolResult{Tool: "tool_parse", Summary: "Malformed tool request rejected", Error: "malformed DeepSeek XML tool request"},
				ToolTransportMode: "native",
			},
			want:            recoverycontract.FailureClassToolParseError,
			needle:          "active native tool-call transport",
			wantNoValidJSON: true,
		},
		{
			name: "permission",
			input: RecoveryInput{
				Reason:  "permission_denied",
				Request: workspace.ToolRequest{Tool: "edit_file", Path: "README.md"},
				Result:  workspace.ToolResult{Tool: "edit_file", Summary: "Write approval required", Error: "approval required before editing README.md"},
			},
			want:   recoverycontract.FailureClassPermissionBoundary,
			needle: "stop and report that write approval is required",
		},
		{
			name: "completion gate",
			input: RecoveryInput{
				Reason:  "completion_gate_rejected",
				Request: workspace.ToolRequest{Tool: "completion_gate"},
				Result:  workspace.ToolResult{Tool: "completion_gate", Summary: "Task ledger completion gate rejected final answer", Error: "completion gate still requires pending work"},
			},
			want:   recoverycontract.FailureClassCompletionGate,
			needle: "satisfy the completion gate before final answer",
		},
		{
			name: "repeated action",
			input: RecoveryInput{
				Reason:  "tool_replayed",
				Request: workspace.ToolRequest{Tool: "read_file", Path: "README.md"},
				Result:  workspace.ToolResult{Tool: "read_file", Summary: "Repeated tool request replayed prior evidence", Error: "this exact request already replayed prior evidence once"},
			},
			want:   recoverycontract.FailureClassRepeatedAction,
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
			if tc.wantNoValidJSON && strings.Contains(strings.Join(plan.ValidNext, "\n"), "valid JSON tool object") {
				t.Fatalf("native recovery valid_next should not ask for textual JSON, got %#v", plan.ValidNext)
			}
			if tc.wantNoValidJSON && strings.Count(strings.Join(plan.ValidNext, "\n"), "active native tool-call transport") != 1 {
				t.Fatalf("native recovery valid_next should contain one native transport action, got %#v", plan.ValidNext)
			}
		})
	}
}

func TestRenderStepInstructionIncludesCurrentState(t *testing.T) {
	instruction := BuildStepInstruction(RunState{
		Lane:           "implement",
		Phase:          "repair",
		Step:           4,
		EvidencePaths:  []string{"internal/surfaces/tui/app/app.go", "internal/surfaces/tui/app/app.go"},
		ChangedPaths:   []string{"internal/surfaces/tui/app/rendering.go"},
		LastTool:       "read_file",
		LastResult:     ToolResultSummary{Tool: "read_file", Error: "blocked"},
		FailureClass:   "guard_rejection",
		NextActions:    []string{"approved-target write capability inside approved paths"},
		InvalidActions: []string{"duplicate_read_file_on_bootstrapped_target"},
	})
	rendered := RenderStepInstruction(instruction)

	for _, needle := range []string{
		"[frontier step instruction]",
		`"lane":"implement"`,
		`"phase":"repair"`,
		`"failure_class":"guard_rejection"`,
		"duplicate_read_file_on_bootstrapped_target",
		"internal/surfaces/tui/app/app.go",
	} {
		if !strings.Contains(rendered, needle) {
			t.Fatalf("expected %q in rendered instruction:\n%s", needle, rendered)
		}
	}

	if strings.Count(rendered, "internal/surfaces/tui/app/app.go") != 1 {
		t.Fatalf("expected duplicate paths to be normalized in rendered instruction:\n%s", rendered)
	}
}

func TestRenderStepInstructionIncludesReadinessState(t *testing.T) {
	instruction := BuildStepInstruction(RunState{
		Lane:  "implement",
		Phase: "evidence",
		Step:  2,
		Readiness: &ReadinessState{
			ReadyToWrite:        true,
			ClosedClaims:        []string{"context_packet_available", "context_packet_available"},
			OpenObligations:     []string{"required_edit_paths_not_changed"},
			EvidenceSources:     []string{"context_packet", "required_edit_paths"},
			FurtherReadsRequire: "one exact edit-safety gap",
		},
	})
	rendered := RenderStepInstruction(instruction)

	for _, want := range []string{
		`"readiness":`,
		`"ready_to_write":true`,
		"context_packet_available",
		"writing is supported by the current packet/evidence",
		"exact edit blocker",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected readiness field %q in:\n%s", want, rendered)
		}
	}

	if strings.Count(rendered, "context_packet_available") != 1 {
		t.Fatalf("expected readiness closed claims to be deduplicated:\n%s", rendered)
	}
}

func TestBuildStepInstructionPrioritizesRequiredEditPathsOverAnswerReadiness(t *testing.T) {
	instruction := BuildStepInstruction(RunState{
		Lane:              "implement",
		TaskFamily:        "implementation",
		Permission:        "guarded_workspace_write",
		RequiredEditPaths: []string{"internal/surfaces/tui/app/prompt_routing.go"},
		Readiness: &ReadinessState{
			ReadyToAnswer: true,
		},
	})
	if !strings.Contains(instruction.Directive, "Required edit paths define the implementation scope") {
		t.Fatalf("expected required-edit-path directive to win, got %q", instruction.Directive)
	}

	if strings.Contains(instruction.Directive, "answering is supported") {
		t.Fatalf("answer readiness should not override required edit paths: %q", instruction.Directive)
	}
}

func TestBuildStepInstructionPrioritizesVerificationOverAnswerReadiness(t *testing.T) {
	instruction := BuildStepInstruction(RunState{
		Lane: "implement",
		Readiness: &ReadinessState{
			ReadyToAnswer: true,
			ReadyToVerify: true,
			OpenObligations: []string{
				"source_aware_verification_pending",
			},
		},
	})
	if !strings.Contains(instruction.Directive, "verification is the open obligation") {
		t.Fatalf("expected verification directive to win, got %q", instruction.Directive)
	}

	if strings.Contains(instruction.Directive, "answering is supported") {
		t.Fatalf("answer readiness should not override verification: %q", instruction.Directive)
	}
}

func TestBuildStepInstructionUsesClosureDirective(t *testing.T) {
	instruction := BuildStepInstruction(RunState{
		Lane:        "main",
		Phase:       "evidence",
		Step:        3,
		Constraints: []string{"child_packet_available"},
		NextActions: []string{"final answer from the child packet evidence is available"},
		InvalidActions: []string{
			"open-ended discovery after a child packet without naming a concrete gap",
		},
	})
	if !strings.Contains(instruction.Directive, "A final answer is available if the evidence supports it") {
		t.Fatalf("expected closure-biased answer availability directive, got %q", instruction.Directive)
	}

	if !strings.Contains(instruction.Directive, "one exact missing claim") || !strings.Contains(instruction.Directive, "one exact probe") {
		t.Fatalf("expected exact-claim/exact-probe language, got %q", instruction.Directive)
	}

	if !strings.Contains(instruction.Directive, "options, not mandatory commands") {
		t.Fatalf("expected closure directive to clarify option-vs-command, got %q", instruction.Directive)
	}
}

func TestBuildStepInstructionUsesWriteReadyImplementationDirective(t *testing.T) {
	instruction := BuildStepInstruction(RunState{
		Lane:              "implement",
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		Permission:        "guarded_workspace_write",
		RequiredEditPaths: []string{"native/src/system.cpp", "native/tests/test_system.cpp"},
		Constraints:       []string{"implementation_packet_write_ready"},
	})
	for _, want := range []string{
		"Required edit paths define the implementation scope",
		"in-scope required-path edit",
		"semantic/context/declared project discovery",
		"named_gap",
		"exact missing anchor or stale fact",
		"<<QUESTION>>",
	} {
		if !strings.Contains(instruction.Directive, want) {
			t.Fatalf("expected write-ready directive %q, got %q", want, instruction.Directive)
		}
	}
}

func TestBuildStepInstructionAddsGenericGuardedMutationContractAfterNoWriteGate(t *testing.T) {
	instruction := BuildStepInstruction(RunState{
		Lane:             "implement",
		TaskFamily:       "implementation",
		Contract:         "implementation_result",
		Permission:       "guarded_workspace_write",
		Phase:            "repair",
		FailureClass:     "completion_gate",
		InvalidActions:   []string{"final_answer_without_satisfying_completion_gate"},
		VerificationPlan: []string{"go test ./internal/runtime/workerruntime"},
		RequiredEvidence: []string{"changed_files_or_noop_reason", "verification_result"},
		Readiness:        &ReadinessState{ReadyToAnswer: true},
		LastResult:       ToolResultSummary{Tool: "completion_gate", Error: "no workspace changes were detected"},
	})

	for _, want := range []string{
		"guarded workspace mutation without required_edit_paths",
		"select the target before writing",
		"source-body evidence",
		"parser-readable",
		"write when the exact target and edit shape are known",
		"bounded discovery/read with a named_gap",
		"<<QUESTION>>/no-op",
		"concrete evidence",
		"After a write, verify",
	} {
		if !strings.Contains(instruction.Directive, want) {
			t.Fatalf("expected generic guarded mutation directive %q, got %q", want, instruction.Directive)
		}
	}

	if strings.Contains(instruction.Directive, "answering is supported") {
		t.Fatalf("generic mutation contract should not be presented as answer-only readiness: %q", instruction.Directive)
	}
}

func TestPlanExecuteAllowedScopeDoesNotUseGenericGuardedMutationContract(t *testing.T) {
	initial := BuildInitialStepInstruction(WorkerRuntimeBrief{
		Lane:              "plan-execute",
		TaskFamily:        "plan_then_execute_planner",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedEditPaths:  []string{"pkg/feature.go", "pkg/feature_test.go"},
		AllowedTools:      []string{"context_pack", "read_file", "submit_plan", "edit_file"},
	})
	if strings.Contains(initial.Directive, "guarded workspace mutation without required_edit_paths") ||
		strings.Contains(initial.Directive, "write when the exact target") {

		t.Fatalf("plan-execute planner should not receive direct mutation target contract:\n%s", initial.Directive)
	}

	step := BuildStepInstruction(RunState{
		Lane:             "plan-execute",
		TaskFamily:       "plan_then_execute_planner",
		Contract:         "implementation_result",
		Permission:       "guarded_workspace_write",
		AllowedEditPaths: []string{"pkg/feature.go", "pkg/feature_test.go"},
		Readiness:        &ReadinessState{ReadyToAnswer: true},
	})
	if strings.Contains(step.Directive, "guarded workspace mutation without required_edit_paths") ||
		strings.Contains(step.Directive, "write when the exact target") {

		t.Fatalf("plan-execute planner step should not receive direct mutation target contract:\n%s", step.Directive)
	}
}

func TestBuildStepInstructionOnlyUsesRemainingPathDirectiveForMissingPaths(t *testing.T) {
	partial := BuildStepInstruction(RunState{
		Lane:              "implement",
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		Permission:        "guarded_workspace_write",
		RequiredEditPaths: []string{"a.txt", "b.txt"},
		ChangedPaths:      []string{"a.txt"},
	})
	if !strings.Contains(partial.Directive, "Required edit paths remain contractual") {
		t.Fatalf("expected remaining-path directive for partial edit, got %q", partial.Directive)
	}

	complete := BuildStepInstruction(RunState{
		Lane:              "implement",
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		Permission:        "guarded_workspace_write",
		RequiredEditPaths: []string{"a.txt", "b.txt"},
		ChangedPaths:      []string{"a.txt", "b.txt"},
	})
	if strings.Contains(complete.Directive, "Required edit paths remain contractual") {
		t.Fatalf("did not expect remaining-path directive once all required paths changed, got %q", complete.Directive)
	}
}

func TestRenderInitialRuntimeBriefIncludesPolicyManifestAndTargets(t *testing.T) {
	rendered := RenderInitialRuntimeBrief(WorkerRuntimeBrief{
		Lane:                 "implement",
		TaskFamily:           "implementation",
		Contract:             "implementation_result",
		Objective:            "Update routing",
		PermissionProfile:    "guarded_workspace_writes",
		TargetRefs:           []string{"internal/surfaces/tui/app/prompt_routing.go"},
		AllowedTools:         []string{"read_file", "edit_file", "shell"},
		ForbiddenTools:       []string{"mutating_shell"},
		RequiredEvidence:     []string{"changed_files_or_noop_reason", "verification_result"},
		ReadinessGate:        []string{"verification_contract_resolved"},
		StopConditions:       []string{"task_complete_or_blocked"},
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
		VerificationPlan:     []string{"go test ./internal/surfaces/tui/app"},
		TargetPacket:         []string{"[implementation target packet]", "primary paths: internal/surfaces/tui/app/prompt_routing.go"},
	})

	for _, needle := range []string{
		"[frontier step instruction]",
		`"objective":"Update routing"`,
		`"allowed_tools":["read_file","edit_file","shell"]`,
		`"evidence_contract":["required evidence before contract fulfillment: changed_files_or_noop_reason, verification_result","readiness gates to close or explicitly block: verification_contract_resolved","terminal stop conditions: task_complete_or_blocked"]`,
		`"tool":"edit_file"`,
		"internal/surfaces/tui/app/prompt_routing.go",
		"go test ./internal/surfaces/tui/app",
	} {
		if !strings.Contains(rendered, needle) {
			t.Fatalf("expected %q in initial runtime brief:\n%s", needle, rendered)
		}
	}
}

func TestRenderInitialRuntimeBriefAddsGuardedWriteFirstActionContract(t *testing.T) {
	rendered := RenderInitialRuntimeBrief(WorkerRuntimeBrief{
		Lane:              "implement",
		TaskFamily:        "docs_edit",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"read_file", "edit_file"},
	})
	for _, want := range []string{
		"first assistant response must be either exactly one tool call",
		"explicit blocker/no-op answer",
		"narrative-only implementation summaries are invalid",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected guarded-write first-action directive %q in:\n%s", want, rendered)
		}
	}

	readOnly := RenderInitialRuntimeBrief(WorkerRuntimeBrief{
		Lane:              "architecture",
		TaskFamily:        "architecture",
		Contract:          "architecture_assessment",
		PermissionProfile: "read_only_workspace",
		AllowedTools:      []string{"search_content", "read_file"},
	})
	if strings.Contains(readOnly, "first assistant response must be either exactly one tool call") {
		t.Fatalf("read-only runtime brief should not include guarded-write first-action directive:\n%s", readOnly)
	}
}

func TestRenderInitialRuntimeBriefAddsGenericGuardedMutationContractWithoutRequiredPaths(t *testing.T) {
	rendered := RenderInitialRuntimeBrief(WorkerRuntimeBrief{
		Lane:                 "implement",
		TaskFamily:           "implementation",
		Contract:             "implementation_result",
		Objective:            "Update behavior",
		PermissionProfile:    "guarded_workspace_write",
		AllowedTools:         []string{"context_pack", "read_file", "edit_file"},
		RequiredEvidence:     []string{"changed_files_or_noop_reason", "verification_result"},
		VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
		Readiness:            &ReadinessState{ReadyToAnswer: true},
	})

	for _, want := range []string{
		"first assistant response must be either exactly one tool call",
		"guarded workspace mutation without required_edit_paths",
		"select the target before writing",
		"source-body evidence",
		"parser-readable",
		"write when the exact target and edit shape are known",
		"bounded discovery/read with a named_gap",
		"<<QUESTION>>/no-op",
		"After a write, verify",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected generic guarded mutation contract %q in:\n%s", want, rendered)
		}
	}

	for _, forbidden := range []string{
		"product workflow",
		"PRD",
		"ADR",
		"brief",
		"dispatch",
		"status migration",
		"answering is supported",
	} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("generic guarded mutation contract should not include %q:\n%s", forbidden, rendered)
		}
	}
}

func TestRenderInitialRuntimeBriefAddsRequiredEditPathReadinessDirective(t *testing.T) {
	rendered := RenderInitialRuntimeBrief(WorkerRuntimeBrief{
		Lane:              "implement",
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		RequiredEditPaths: []string{"native/src/system.cpp"},
		AllowedTools:      []string{"read_file", "edit_file"},
	})
	for _, want := range []string{
		"required_edit_paths are present",
		"in-scope required-path edit",
		"semantic/context/declared project discovery",
		"named_gap",
		"exact blocker",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected required-path readiness directive %q in:\n%s", want, rendered)
		}
	}
}

func TestRenderInitialRuntimeBriefUsesPlanExecuteSubmitPlanDirective(t *testing.T) {
	rendered := RenderInitialRuntimeBrief(WorkerRuntimeBrief{
		Lane:              "plan-execute",
		TaskFamily:        "plan_then_execute_planner",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"read_file", "submit_plan"},
		RequiredTools:     []string{"submit_plan"},
		RequiredEditPaths: []string{"internal/surfaces/tui/app/rendering.go"},
		SearchPolicy:      []string{"submit_plan_requires_executable_final_edits"},
	})
	for _, want := range []string{
		"plan-execute planner phase",
		"submit_plan coverage scope",
		"not permission for direct edit tools",
		"parser-readable submit_plan",
		"complete executable final edit bodies",
		"Decision: source-satisfied",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("plan-execute runtime brief missing %q:\n%s", want, rendered)
		}
	}

	if strings.Contains(rendered, "in-scope required-path edit") {
		t.Fatalf("plan-execute runtime brief must not use implementation edit wording:\n%s", rendered)
	}
}

func TestRenderInitialRuntimeBriefAddsDeterministicDiscoveryExamples(t *testing.T) {
	rendered := RenderInitialRuntimeBrief(WorkerRuntimeBrief{
		Lane:              "architecture",
		TaskFamily:        "architecture",
		Contract:          "architecture_assessment",
		PermissionProfile: "read_only_workspace",
		AllowedTools:      []string{"search_content", "read_file", "code_navigate", "read_only_capability", "discovery_query"},
	})

	for _, needle := range []string{
		"Available evidence capabilities",
		`"available_evidence_capabilities"`,
		`"discovery_examples"`,
		"For broad architecture, impact, tracing, security, or history questions",
		"bounded discovery-packet capability",
		"choose purpose from the tool schema",
		"find_definition",
		"read_only_capability",
	} {
		if !strings.Contains(rendered, needle) {
			t.Fatalf("expected %q in deterministic discovery guidance:\n%s", needle, rendered)
		}
	}

	if strings.Contains(rendered, "architecture_boundary shape") || strings.Contains(rendered, "security_review:") {
		t.Fatalf("runtime brief should not repeat verbose discovery_query examples:\n%s", rendered)
	}

	for _, forbidden := range []string{
		"use one bounded discovery_query",
		"run context_pack",
		"call go_definition",
		"invoke read_only_capability",
	} {
		if strings.Contains(strings.ToLower(rendered), strings.ToLower(forbidden)) {
			t.Fatalf("runtime brief should avoid prescriptive raw-tool phrase %q:\n%s", forbidden, rendered)
		}
	}
}

func TestNormalizeRuntimeBriefSummarizesCapabilitiesWithoutLanguageAdapterList(t *testing.T) {
	brief := NormalizeRuntimeBrief(WorkerRuntimeBrief{
		Lane:              "architecture",
		TaskFamily:        "architecture",
		Contract:          "architecture_assessment",
		PermissionProfile: "read_only_workspace",
		AllowedTools: []string{
			"code_navigate",
			"read_only_capability",
			"go_definition",
			"go_references",
			"python_read_symbol",
			"read_file",
		},
	})

	if len(brief.EvidenceCapabilities) == 0 {
		t.Fatalf("expected compact capability summary, got %#v", brief)
	}

	joined := strings.Join(brief.EvidenceCapabilities, "\n")
	for _, want := range []string{"inspect_file", "inspect_symbol", "find_definition", "find_references", workspace.CapabilityClassCompatibilityAdapter} {
		if !strings.Contains(joined, want) {
			t.Fatalf("capability summary missing %q:\n%s", want, joined)
		}
	}

	for _, verboseAdapter := range []string{"go_definition", "go_references", "python_read_symbol", "language adapters", "tools:"} {
		if strings.Contains(joined, verboseAdapter) {
			t.Fatalf("capability summary should collapse language adapter %q:\n%s", verboseAdapter, joined)
		}
	}
}

func TestRenderInitialRuntimeBriefSuppressesDiscoveryExamplesForWriteReadyPacket(t *testing.T) {
	rendered := RenderInitialRuntimeBrief(WorkerRuntimeBrief{
		Lane:              "implement",
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"read_file", "edit_file", "discovery_query"},
		RequiredEditPaths: []string{"native/tests/test_energy.cpp"},
		TargetPacket:      []string{"[implementation target packet]", "verified anchor: native/tests/test_energy.cpp"},
	})
	if strings.Contains(rendered, `"discovery_examples"`) || strings.Contains(rendered, "use one bounded discovery_query") {
		t.Fatalf("write-ready implementation packet should not advertise discovery examples:\n%s", rendered)
	}

	if !strings.Contains(rendered, "required_edit_paths are present") {
		t.Fatalf("expected write-ready required-path directive to remain:\n%s", rendered)
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

func TestBuildInitialStepInstructionConversationNamesDefaultReadOnlyAnswerLane(t *testing.T) {
	step := BuildInitialStepInstruction(WorkerRuntimeBrief{
		Lane:              "main",
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		AllowedTools:      []string{"search_content", "read_file", "context_query"},
	})
	if !strings.Contains(step.Directive, "Conversation lane") ||
		!strings.Contains(step.Directive, "every response must either") ||
		!strings.Contains(step.Directive, "Choose from the available evidence capabilities according to the specific gap") ||
		!strings.Contains(step.Directive, "Do not return raw tool summaries") {

		t.Fatalf("conversation initial directive should explain the default read-only answer contract:\n%s", step.Directive)
	}

	for _, forbidden := range []string{"10-35", "prefer this order", "Use context_pack to localize", "Use the note tool"} {
		if strings.Contains(step.Directive, forbidden) {
			t.Fatalf("conversation initial directive should not prescribe strategy cue %q:\n%s", forbidden, step.Directive)
		}
	}

	for _, want := range []string{
		"Evidence contract / exit criteria are summarized in current_state.evidence_contract",
		"localization/context packets scope candidates but do not complete evidence by themselves",
		"source, symbol, narrow line-range, read_symbol/extract_body, or routed source-body evidence",
		"answer once inspected evidence resolves the question",
		"one exact unresolved claim and one bounded probe",
	} {
		if !strings.Contains(step.Directive+"\n"+strings.Join(step.CurrentState.EvidenceContract, "\n"), want) {
			t.Fatalf("conversation initial evidence contract should include %q:\ndirective=%s\ncontract=%#v", want, step.Directive, step.CurrentState.EvidenceContract)
		}
	}

	if strings.Contains(step.Directive, "The readiness state says answering is supported") {
		t.Fatalf("conversation initial directive must not reintroduce premature readiness wording:\n%s", step.Directive)
	}
}

func TestRenderInitialRuntimeBriefConversationRendersEvidenceContractBeforeFirstAction(t *testing.T) {
	rendered := RenderInitialRuntimeBrief(WorkerRuntimeBrief{
		Lane:              "main",
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		Objective:         "Does internal/runtime/agent/runner.go allow answering after evidence?",
		PermissionProfile: "read_only_workspace",
		AllowedTools:      []string{"context_pack", "code_navigate", "read_file"},
	})

	for _, want := range []string{
		`"evidence_contract"`,
		"localization/context packets scope candidates but do not complete evidence by themselves",
		"source, symbol, narrow line-range",
		"read_symbol/extract_body",
		"routed source-body evidence",
		"answer once inspected evidence resolves the question",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered initial conversation brief should include %q:\n%s", want, rendered)
		}
	}

	if strings.Contains(rendered, "The readiness state says answering is supported") {
		t.Fatalf("rendered initial conversation brief must not claim answer readiness:\n%s", rendered)
	}
}

func TestBuildInitialStepInstructionRequiresEvidenceToolWhenReadOnlyGateOpen(t *testing.T) {
	step := BuildInitialStepInstruction(WorkerRuntimeBrief{
		Lane:              "main",
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		AllowedTools:      []string{"read_file", "search_content", "context_pack"},
		Readiness:         &ReadinessState{ReadyToAnswer: false, OpenObligations: []string{"evidence"}},
	})

	for _, want := range []string{
		"read-only workspace evidence gate is open",
		"first assistant response must be exactly one parser-readable bounded evidence tool request",
		"prose/final answer is valid only after the evidence tool returns",
	} {
		if !strings.Contains(step.Directive, want) {
			t.Fatalf("expected %q in read-only evidence-pending directive:\n%s", want, step.Directive)
		}
	}
}

func TestInitialActionContractAppliesUsesExactMutationContracts(t *testing.T) {
	if initialActionContractApplies(RunState{TaskFamily: "implementation_notes", Contract: "direct_answer", Permission: "read_only_workspace"}) {
		t.Fatal("read-only implementation-adjacent wording must not activate a mutation contract")
	}

	if !initialActionContractApplies(RunState{TaskFamily: "implementation", Contract: "implementation_result", Permission: "guarded_workspace_write"}) {
		t.Fatal("exact implementation contract should activate mutation contract")
	}
}

func TestNormalizeRuntimeBriefConversationOnlyClearsToolSurface(t *testing.T) {
	brief := NormalizeRuntimeBrief(WorkerRuntimeBrief{
		PermissionProfile: "conversation_only",
		AllowedTools:      []string{"task", "search_files"},
		RequiredTools:     []string{"search_files"},
		DiscoveryExamples: []string{"discovery-packet capability available"},
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

func TestNormalizeRuntimeBriefSuppressesPlaceholderManifestRequests(t *testing.T) {
	brief := NormalizeRuntimeBrief(WorkerRuntimeBrief{
		AllowedTools: []string{"submit_plan", "pwd"},
		ToolManifest: []ToolManifestEntry{
			{
				Tool:    "submit_plan",
				Request: `{"tool":"submit_plan","rationale":"one paragraph on the overall plan","edits":[{"path":"relative/file","old_text":"exact current substring","new_text":"complete replacement text","purpose":"why this edit"}]}`,
				Purpose: "submit a structured executable edit plan",
			},
			{
				Tool:    "pwd",
				Request: `{"tool":"pwd"}`,
				Purpose: "show current workspace directory",
			},
		},
	})

	byTool := map[string]ToolManifestEntry{}
	for _, entry := range brief.ToolManifest {
		byTool[entry.Tool] = entry
	}

	if got := byTool["submit_plan"].Request; got != "" {
		t.Fatalf("placeholder submit_plan request should be suppressed, got %q", got)
	}

	if got := byTool["pwd"].Request; got == "" {
		t.Fatal("concrete request should be preserved")
	}
}

func TestNormalizeRuntimeBriefBuildsWorkspaceOwnedToolManifest(t *testing.T) {
	brief := NormalizeRuntimeBrief(WorkerRuntimeBrief{AllowedTools: []string{"submit_plan", "pwd"}})

	byTool := map[string]ToolManifestEntry{}
	for _, entry := range brief.ToolManifest {
		byTool[entry.Tool] = entry
	}

	if got := byTool["submit_plan"].Request; got != "" {
		t.Fatalf("workspace placeholder request should be suppressed at runtime boundary, got %q", got)
	}

	if got := byTool["pwd"].Request; got == "" {
		t.Fatal("concrete request should remain available")
	}
}

func TestRoleSpecForChildWriteCarriesIsolatedContract(t *testing.T) {
	spec := RoleSpecFor("workbranch_write")
	if spec.Name != RoleChildWrite || spec.PermissionProfile != PermissionIsolatedWorkbranchWrite {
		t.Fatalf("unexpected child write role: %#v", spec)
	}

	for _, want := range []string{"edit_file", "apply_patch", "go_move_declarations", "shell"} {
		if !slices.Contains(spec.AllowedTools, want) {
			t.Fatalf("expected child write role to allow %q, got %#v", want, spec.AllowedTools)
		}
	}

	for _, want := range []string{"task", "git_history_mutation", "git_index_mutation", "git_push", "publish", "deploy"} {
		if !slices.Contains(spec.ForbiddenTools, want) {
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
	if spec.Name != RoleChildReadOnly || spec.PermissionProfile != PermissionReadOnlyWorkspace {
		t.Fatalf("unexpected child read-only role: %#v", spec)
	}

	for _, forbidden := range []string{"task", "list_files"} {
		if slices.Contains(spec.AllowedTools, forbidden) {
			t.Fatalf("child read-only role must not advertise %q, got %#v", forbidden, spec.AllowedTools)
		}
	}

	for _, want := range []string{"read_file", "search_files", "search_content", "file_stats", "code_navigate", "read_only_capability", "context_pack", "discovery_query", "structural_search"} {
		if !slices.Contains(spec.AllowedTools, want) {
			t.Fatalf("expected child read-only role to allow %q, got %#v", want, spec.AllowedTools)
		}
	}

	for _, want := range []string{"answer_when_evidence_is_sufficient", "if_not_answering_name_one_exact_missing_claim_and_probe", "prefer_symbol_or_handle_probes_over_raw_path_reads"} {
		if !runtimeLabelContains(spec.SearchPolicy, want) {
			t.Fatalf("expected child read-only search policy to include %q, got %#v", want, spec.SearchPolicy)
		}
	}
}

func TestRoleSpecForImplementTrustsPreloadedAnchors(t *testing.T) {
	spec := RoleSpecFor("implement")
	for _, want := range []string{"consume_target_packet_first", "trust_preloaded_current_state_anchors", "write_once_edit_location_is_supported"} {
		if !runtimeLabelContains(spec.SearchPolicy, want) {
			t.Fatalf("expected implement search policy to include %q, got %#v", want, spec.SearchPolicy)
		}
	}
}

func TestRoleSpecForMainUsesConversationOnlyFallback(t *testing.T) {
	spec := RoleSpecFor("main")
	if spec.Name != RoleMain || spec.PermissionProfile != PermissionConversationOnly {
		t.Fatalf("unexpected main role: %#v", spec)
	}

	if len(spec.AllowedTools) != 0 {
		t.Fatalf("main role should not advertise tools by default, got %#v", spec.AllowedTools)
	}
}
