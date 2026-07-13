package workerruntime

import (
	"fmt"
	"slices"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
	contractcompile "idiom/internal/contracts/compile"
	recoverycontract "idiom/internal/contracts/recovery"
)

var _ = ginkgo.It("build action guidance for bootstrapped read blocker", func() {
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

	gomega.Expect(slices.Contains(guidance.Constraints, "declaration_moves_forbidden")).To(gomega.BeTrue(), fmt.Sprintf("missing declaration move constraint: %#v", guidance))

	gomega.Expect(slices.Contains(guidance.Constraints, "bootstrap_evidence_available")).To(gomega.BeTrue(), fmt.Sprintf("missing bootstrap evidence constraint: %#v", guidance))

	gomega.Expect(slices.Contains(guidance.InvalidNext, "duplicate_read_file_on_bootstrapped_target")).To(gomega.BeFalse(), fmt.Sprintf("bootstrapped evidence must not make an otherwise authorized read invalid: %#v", guidance))

	joined := strings.Join(guidance.ValidNext, "\n")
	gomega.Expect(joined).To(gomega.ContainSubstring("approved-target write capability"), fmt.Sprintf("missing expected valid next actions: %#v", guidance.ValidNext))
	gomega.Expect(joined).To(gomega.ContainSubstring("narrow range read"), fmt.Sprintf("missing expected valid next actions: %#v", guidance.ValidNext))
})

var _ = ginkgo.It("render active protocol card uses compiled projection", func() {
	rendered := RenderActiveProtocolCard(contractcompile.ModelProtocolProjection{
		ContractID:                    "contract-1",
		ProtocolHash:                  "hash-123",
		PhaseID:                       "required_source",
		VisibleTools:                  []string{"submit_plan", "read_file", "read_file", "code_navigate"},
		CallableTools:                 []string{"submit_plan", "code_navigate"},
		CallableActionClasses:         []string{"read", "submit_plan"},
		TerminalResponseShapes:        []string{"submit_plan", "<<QUESTION>>:", "Decision: source-satisfied"},
		InvalidActionClasses:          []string{"submit_plan", "generic_probe"},
		RequiredEditCoverage:          []string{"internal/a.go"},
		SourceBodyObligations:         []string{"internal/a.go"},
		GroundedSourceBodyPaths:       []string{"docs/architecture.md"},
		GroundedSourceBodyPathTotal:   3,
		GroundedSourceBodyPathOmitted: 2,
		CandidateOnlyPaths:            []string{"internal/candidate.go"},
		CandidateOnlyPathTotal:        2,
		CandidateOnlyPathOmitted:      1,
		PathRoleSummary:               []string{"required_edit_path: internal/a.go"},
		VerificationObligations:       []string{"go test ./..."},
		SafeAlternatives:              []string{"generic_probe", "<<QUESTION>>:"},
	})

	for _, want := range []string{
		"[active contract protocol]",
		"Active contract: contract-1 / protocol_hash=hash-123",
		"Phase: required_source",
		"Visible tools: [submit_plan, code_navigate, read_file]",
		"Callable tools now: [submit_plan, code_navigate]",
		"Callable action classes now: [submit_plan, read]",
		"Valid terminal outcomes: Decision: source-satisfied; <<QUESTION>>:",
		"Invalid now: submit_plan, generic_probe",
		"Path roles: required_edit_paths=[internal/a.go]; source_body_obligations=[internal/a.go]; roles=[required_edit_path: internal/a.go]",
		"Source evidence: grounded_body_paths=[docs/architecture.md] (total=3, omitted=2); pending_body_obligations=[internal/a.go] (total=1, omitted=0); candidate_or_outline_only_paths=[internal/candidate.go] (total=2, omitted=1) (candidate/search/outline paths are not source-body evidence)",
		"Verification: go test ./...",
		"Safe alternatives: <<QUESTION>>:; generic_probe",
	} {
		gomega.Expect(strings.Contains(rendered, want)).To(gomega.BeTrue(), fmt.Sprintf("missing %q in rendered protocol card:\n%s", want, rendered))
	}

	gomega.Expect(strings.Contains(rendered, "submit_plan; Decision: source-satisfied")).To(gomega.BeFalse(), fmt.Sprintf("terminal outcomes should suppress submit_plan when submit_plan is invalid:\n%s", rendered))
})

var _ = ginkgo.It("render active protocol card omits empty projection", func() {
	if rendered := RenderActiveProtocolCard(contractcompile.ModelProtocolProjection{ContractID: "contract-1"}); rendered != "" {
		ginkgo.Fail(fmt.Sprintf("empty protocol hash should not render card, got %q", rendered))
	}
})

var _ = ginkgo.It("normalize readiness state returns independent copy", func() {
	original := &ReadinessState{
		ReadyToAnswer:       true,
		ClosedClaims:        []string{" claim ", "claim"},
		OpenObligations:     []string{"evidence"},
		EvidenceSources:     []string{"read_file:a.go"},
		FurtherReadsRequire: " source body ",
	}

	normalized := NormalizeReadinessState(original)
	gomega.Expect(normalized).NotTo(gomega.BeNil(), fmt.Sprint("expected non-empty readiness state"))
	gomega.Expect(normalized == original).To(gomega.BeFalse(), fmt.Sprint("normalization should return a distinct readiness state"))
	gomega.Expect(slices.Equal(normalized.ClosedClaims, []string{"claim"})).To(gomega.BeTrue(), fmt.Sprintf("closed claims = %#v", normalized.ClosedClaims))
	gomega.Expect(normalized.FurtherReadsRequire != "source body").To(gomega.BeFalse(), fmt.Sprintf("further reads requirement = %q", normalized.FurtherReadsRequire))

	original.ClosedClaims[0] = "mutated"
	original.OpenObligations[0] = "mutated"
	original.EvidenceSources[0] = "mutated"
	gomega.Expect(normalized.ClosedClaims).NotTo(gomega.ContainElement("mutated"), fmt.Sprintf("normalized readiness shares backing storage with original: %#v", normalized))
	gomega.Expect(normalized.OpenObligations).NotTo(gomega.ContainElement("mutated"), fmt.Sprintf("normalized readiness shares backing storage with original: %#v", normalized))
	gomega.Expect(normalized.EvidenceSources).NotTo(gomega.ContainElement("mutated"), fmt.Sprintf("normalized readiness shares backing storage with original: %#v", normalized))
})

var _ = ginkgo.It("typed id string converters trim and preserve order", func() {
	if got := EvidenceRequirementStrings([]EvidenceRequirementID{
		" first ",
		"",
		"second",
		"first",
	}); !slices.Equal(got, []string{"first", "second", "first"}) {
		ginkgo.Fail(fmt.Sprintf("evidence requirement strings = %#v", got))
	}

	if got := ReadinessGateStrings([]ReadinessGateID{
		" ready ",
		"ready",
		" ",
		"verify",
	}); !slices.Equal(got, []string{"ready", "ready", "verify"}) {
		ginkgo.Fail(fmt.Sprintf("readiness gate strings = %#v", got))
	}

	if got := typedIDStrings([]SearchPolicyID{
		" search ",
		"",
		"preserve",
		"search",
	}); !slices.Equal(got, []string{"search", "preserve", "search"}) {
		ginkgo.Fail(fmt.Sprintf("search policy strings = %#v", got))
	}

	if got := typedIDStrings([]StopConditionID{
		" stop ",
		"stop",
		"",
		"done",
	}); !slices.Equal(got, []string{"stop", "stop", "done"}) {
		ginkgo.Fail(fmt.Sprintf("stop condition strings = %#v", got))
	}

	if got := typedIDStrings([]FinalCriticID{
		" cite ",
		"",
		"verify",
		"cite",
	}); !slices.Equal(got, []string{"cite", "verify", "cite"}) {
		ginkgo.Fail(fmt.Sprintf("final critic strings = %#v", got))
	}
})

var _ = ginkgo.It("is zero hit search result detects routed symbol miss", func() {
	result := workspace.ToolResult{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output:  "capability: read_symbol\nlanguage: go\nrouted_tool: go_read_symbol\nroute_summary: No Go symbols matched \"CoverageGate\"",
	}

	gomega.Expect(IsZeroHitSearchResult(result)).To(gomega.BeTrue(), fmt.Sprintf("routed symbol miss should count as a zero-hit result"))
})

var _ = ginkgo.It("is zero hit search result detects content search miss", func() {
	result := workspace.ToolResult{
		Tool:    "search_content",
		Summary: `No content matches for "pkey.KeyArrowUp"`,
	}
	gomega.Expect(IsZeroHitSearchResult(result)).To(gomega.BeTrue(), fmt.Sprint("expected No content matches summary to count as zero-hit search result"))
})

var _ = ginkgo.It("is zero hit search result ignores body text", func() {
	result := workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read docs/search-notes.md lines 1-8",
		Output:  "1:The UI should display the phrase \"no matches\" when search is empty.\n2:Found 0 should be documented as copy, not a tool result.",
	}

	gomega.Expect(IsZeroHitSearchResult(result)).To(gomega.BeFalse(), fmt.Sprintf("source body text should not be classified as zero-hit metadata"))
})

var _ = ginkgo.It("build action guidance for exact anchor write failure", func() {
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

	gomega.Expect(slices.Contains(guidance.InvalidNext, "read/search before resolving exact-anchor write failure")).To(gomega.BeFalse(), fmt.Sprintf("authorized inspection must not be invalidated by exact-anchor recovery: %#v", guidance))

	joined := strings.Join(guidance.ValidNext, "\n")
	for _, want := range []string{
		"retry one corrected write shape from the target packet",
		"<<QUESTION>> naming the missing anchor",
		"permission-authorized inspection remains callable",
	} {
		gomega.Expect(strings.Contains(joined, want)).To(gomega.BeTrue(), fmt.Sprintf("expected %q in exact-anchor guidance, got %#v", want, guidance.ValidNext))
	}

	plan := BuildRecoveryPlan(RecoveryInput{
		Reason:       "tool_result",
		Request:      req,
		Result:       result,
		Constraints:  []string{"exact_anchor_implementation"},
		WriteCapable: true,
	})
	gomega.Expect(plan.FailureClass != "exact_anchor_write_failure").To(gomega.BeFalse(), fmt.Sprintf("FailureClass = %q, want exact_anchor_write_failure in %#v", plan.FailureClass, plan))

	recovery := strings.Join(append(append([]string{}, plan.DoNotRepeat...), plan.RequiredNext...), "\n")
	for _, want := range []string{"<<QUESTION>> naming the missing anchor"} {
		gomega.Expect(strings.Contains(recovery, want)).To(gomega.BeTrue(), fmt.Sprintf("expected %q in exact-anchor recovery, got %#v", want, plan))
	}
	gomega.Expect(recovery).NotTo(gomega.ContainSubstring("read/search after an exact-anchor write failed"))
})

var _ = ginkgo.It("build action guidance for exact anchor hidden prewrite tool", func() {
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

	gomega.Expect(slices.Contains(guidance.InvalidNext, "read/search/shell before first exact-anchor write")).To(gomega.BeFalse(), fmt.Sprintf("authorized inspection must not be invalidated before an exact-anchor write: %#v", guidance))

	joined := strings.Join(guidance.ValidNext, "\n")
	for _, want := range []string{
		"exact-anchor write capability with the supplied target-packet anchor",
		"<<QUESTION>> if no exact write anchor or patch is present",
		"permission-authorized inspection remains callable",
	} {
		gomega.Expect(strings.Contains(joined, want)).To(gomega.BeTrue(), fmt.Sprintf("expected %q in exact-anchor hidden-tool guidance, got %#v", want, guidance.ValidNext))
	}

	plan := BuildRecoveryPlan(RecoveryInput{
		Reason:       "tool_rejected",
		Request:      req,
		Result:       result,
		Constraints:  []string{"exact_anchor_implementation"},
		WriteCapable: true,
	})
	gomega.Expect(plan.FailureClass != "exact_anchor_prewrite_hidden_tool").To(gomega.BeFalse(), fmt.Sprintf("FailureClass = %q, want exact_anchor_prewrite_hidden_tool in %#v", plan.FailureClass, plan))

	recovery := strings.Join(append(append([]string{}, plan.DoNotRepeat...), plan.RequiredNext...), "\n")
	for _, want := range []string{"exact-anchor write capability with the supplied anchor"} {
		gomega.Expect(strings.Contains(recovery, want)).To(gomega.BeTrue(), fmt.Sprintf("expected %q in exact-anchor hidden-tool recovery, got %#v", want, plan))
	}
	gomega.Expect(recovery).NotTo(gomega.ContainSubstring("read/search/shell before first exact-anchor write"))
})

var _ = ginkgo.It("build action guidance for plan execute submit ready", func() {
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

	gomega.Expect(slices.Contains(guidance.InvalidNext, "read/search/source-refresh after plan-execute submit_plan readiness")).To(gomega.BeFalse(), fmt.Sprintf("submit readiness must not invalidate authorized inspection: %#v", guidance.InvalidNext))

	joined := strings.Join(guidance.ValidNext, "\n")
	for _, want := range []string{
		"submit_plan covering every required_edit_paths entry with a real edit or source_satisfied_paths entry",
		"Decision: source-satisfied",
		"<<QUESTION>>",
		"permission-authorized inspection remains callable",
	} {
		gomega.Expect(strings.Contains(joined, want)).To(gomega.BeTrue(), fmt.Sprintf("expected %q in submit-ready guidance, got %#v", want, guidance.ValidNext))
	}

	gomega.Expect(strings.Contains(joined, "one exact tool-shaped follow-up")).To(gomega.BeFalse(), fmt.Sprintf("submit-ready guidance must not fall back to generic probe advice: %#v", guidance.ValidNext))

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
		ginkgo.By(tc.name)
		func() {
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
				gomega.Expect(strings.Contains(joined, want)).To(gomega.BeTrue(), fmt.Sprintf("expected %q in submit-ready guidance, got %#v", want, guidance.ValidNext))
			}

			for _, forbidden := range []string{
				"one exact missing claim",
				"narrow range read",
				"exact-text search",
				"answer if the inspected body",
				"one exact tool-shaped follow-up",
			} {
				gomega.Expect(strings.Contains(joined, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("submit-ready guidance leaked read/probe action %q: %#v", forbidden, guidance.ValidNext))
			}
		}()
	}

	plan := BuildRecoveryPlan(RecoveryInput{
		Reason:      "tool_rejected",
		Request:     req,
		Result:      result,
		Constraints: []string{"plan_then_execute_submit_plan_ready"},
	})
	joinedPlan := strings.Join(plan.ValidNext, "\n")

	gomega.Expect(strings.Contains(joinedPlan, "one exact tool-shaped follow-up")).To(gomega.BeFalse(), fmt.Sprintf("submit-ready recovery must not suggest another probe: %#v", plan.ValidNext))

	gomega.Expect(strings.Contains(joinedPlan, "submit_plan covering every required_edit_paths entry with a real edit or source_satisfied_paths entry")).To(gomega.BeTrue(), fmt.Sprintf("submit-ready recovery must keep submit_plan closeout, got %#v", plan.ValidNext))

	parseGuidance := BuildActionGuidance(ActionGuidanceInput{
		Reason:      "tool_parse_rejected",
		Request:     workspace.ToolRequest{Tool: "tool_parse"},
		Result:      workspace.ToolResult{Tool: "tool_parse", Summary: "Malformed tool request rejected", Error: "decode tool request"},
		Constraints: []string{"plan_then_execute_submit_plan_ready"},
	})
	joinedParse := strings.Join(parseGuidance.ValidNext, "\n")

	gomega.Expect(strings.Contains(joinedParse, "parser-readable submit_plan")).To(gomega.BeTrue(), fmt.Sprintf("submit-ready parse recovery should require parser-readable submit_plan, got %#v", parseGuidance.ValidNext))

	gomega.Expect(strings.Contains(joinedParse, "answer normally only if the task is complete")).To(gomega.BeFalse(), fmt.Sprintf("submit-ready parse recovery must not use generic parse fallback, got %#v", parseGuidance.ValidNext))

	parsePlan := BuildRecoveryPlan(RecoveryInput{
		Reason:      "tool_parse_rejected",
		Request:     workspace.ToolRequest{Tool: "tool_parse"},
		Result:      workspace.ToolResult{Tool: "tool_parse", Summary: "Malformed tool request rejected", Error: "decode tool request"},
		Constraints: []string{"plan_then_execute_submit_plan_ready"},
	})
	joinedRequired := strings.Join(parsePlan.RequiredNext, "\n")

	gomega.Expect(strings.Contains(joinedRequired, "parser-readable submit_plan")).To(gomega.BeTrue(), fmt.Sprintf("submit-ready recovery plan should require parser-readable submit_plan, got %#v", parsePlan.RequiredNext))

	gomega.Expect(strings.Contains(joinedRequired, "valid JSON tool object")).To(gomega.BeFalse(), fmt.Sprintf("submit-ready recovery plan must not use generic JSON recovery, got %#v", parsePlan.RequiredNext))

	nativeParseGuidance := BuildActionGuidance(ActionGuidanceInput{
		Reason:            "tool_parse_rejected",
		Request:           workspace.ToolRequest{Tool: "tool_parse"},
		Result:            workspace.ToolResult{Tool: "tool_parse", Summary: "Malformed tool request rejected", Error: "malformed DeepSeek XML tool request"},
		Constraints:       []string{"plan_then_execute_submit_plan_ready"},
		ToolTransportMode: "native",
	})
	joinedNativeGuidance := strings.Join(nativeParseGuidance.ValidNext, "\n")
	gomega.Expect(strings.Contains(joinedNativeGuidance, "active native submit_plan tool call")).To(gomega.BeTrue(), fmt.Sprintf("native submit-ready parse recovery should preserve native submit_plan transport, got %#v", nativeParseGuidance.ValidNext))
	gomega.Expect(strings.Contains(joinedNativeGuidance, "parser-readable submit_plan JSON object")).To(gomega.BeFalse(), fmt.Sprintf("native submit-ready parse recovery must not ask for textual submit_plan JSON, got %#v", nativeParseGuidance.ValidNext))

	nativeParsePlan := BuildRecoveryPlan(RecoveryInput{
		Reason:            "tool_parse_rejected",
		Request:           workspace.ToolRequest{Tool: "tool_parse"},
		Result:            workspace.ToolResult{Tool: "tool_parse", Summary: "Malformed tool request rejected", Error: "malformed DeepSeek XML tool request"},
		Constraints:       []string{"plan_then_execute_submit_plan_ready"},
		ToolTransportMode: "native",
	})
	joinedNativeRequired := strings.Join(nativeParsePlan.RequiredNext, "\n")
	gomega.Expect(strings.Contains(joinedNativeRequired, "active native submit_plan tool call")).To(gomega.BeTrue(), fmt.Sprintf("native submit-ready recovery plan should preserve native submit_plan transport, got %#v", nativeParsePlan.RequiredNext))
	gomega.Expect(strings.Contains(joinedNativeRequired, "parser-readable submit_plan JSON object")).To(gomega.BeFalse(), fmt.Sprintf("native submit-ready recovery plan must not ask for textual submit_plan JSON, got %#v", nativeParsePlan.RequiredNext))
	joinedNativeValid := strings.Join(nativeParsePlan.ValidNext, "\n")
	gomega.Expect(strings.Contains(joinedNativeValid, "active native submit_plan tool call")).To(gomega.BeTrue(), fmt.Sprintf("native submit-ready recovery plan should keep submit_plan-specific valid next action, got %#v", nativeParsePlan.ValidNext))
	gomega.Expect(strings.Contains(joinedNativeValid, "active native tool-call transport for one callable tool")).To(gomega.BeFalse(), fmt.Sprintf("native submit-ready recovery plan must not use generic native tool action, got %#v", nativeParsePlan.ValidNext))
})

var _ = ginkgo.It("build step instruction for plan execute submit ready", func() {
	inst := BuildStepInstruction(RunState{
		TaskFamily:        "plan_then_execute_planner",
		Constraints:       []string{"plan_then_execute_submit_plan_ready"},
		RequiredEditPaths: []string{"a.go", "a_test.go"},
	})

	gomega.Expect(strings.Contains(inst.Directive, "source obligations are closed")).To(gomega.BeTrue(), fmt.Sprintf("expected submit-ready directive, got %q", inst.Directive))

	gomega.Expect(strings.Contains(inst.Directive, "narrow required-path source read")).To(gomega.BeFalse(), fmt.Sprintf("submit-ready directive must not advertise source reads, got %q", inst.Directive))
})

var _ = ginkgo.It("build action guidance for write ready implementation packet", func() {
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
	gomega.Expect(slices.Contains(guidance.InvalidNext, "orientation read/search after required edit paths and packet evidence without a named missing fact")).To(gomega.BeFalse(), fmt.Sprintf("write readiness must not invalidate authorized inspection: %#v", guidance))

	joined := strings.Join(guidance.ValidNext, "\n")
	for _, want := range []string{
		"required-path write capability",
		"required-path edit from the packet and inspected evidence",
		"permission-authorized inspection remains callable",
		"<<QUESTION>> naming the exact edit-safety fact",
	} {
		gomega.Expect(strings.Contains(joined, want)).To(gomega.BeTrue(), fmt.Sprintf("expected %q in write-ready guidance, got %#v", want, guidance.ValidNext))
	}

	gomega.Expect(strings.Contains(joined, "one more targeted probe")).To(gomega.BeFalse(), fmt.Sprintf("write-ready guidance must not suggest another probe, got %#v", guidance.ValidNext))
})

var _ = ginkgo.It("build action guidance for required path gate does not nudge more reads", func() {
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

	gomega.Expect(strings.Contains(joined, "gather or edit the missing evidence")).To(gomega.BeFalse(), fmt.Sprintf("required-path gate should not suggest gathering more evidence, got %#v", guidance.ValidNext))

	for _, want := range []string{
		"satisfy the missing write contract with an allowed edit",
		"answer only after the gate condition is satisfied",
	} {
		gomega.Expect(strings.Contains(joined, want)).To(gomega.BeTrue(), fmt.Sprintf("expected %q in required-path gate guidance, got %#v", want, guidance.ValidNext))
	}
})

var _ = ginkgo.It("build action guidance ignores completion gate phrases in search output", func() {
	guidance := BuildActionGuidance(ActionGuidanceInput{
		Reason:  "tool_result",
		Request: workspace.ToolRequest{Tool: "search_content", Path: "internal/runtime/agent/runner.go"},
		Result: workspace.ToolResult{
			Tool:    "search_content",
			Summary: "Found 20 matching line(s)",
			Output:  "internal/runtime/agent/runner.go:123: completion gate\ninternal/runtime/agent/runner.go:124: final answer rejected\n",
		},
	})
	gomega.Expect(slices.Contains(guidance.Constraints, "completion_gate_active")).To(gomega.BeFalse(), fmt.Sprintf("search output should not fabricate completion-gate state: %#v", guidance))

	gomega.Expect(slices.Contains(guidance.InvalidNext, "final_answer_without_satisfying_completion_gate")).To(gomega.BeFalse(), fmt.Sprintf("search output should not block answers as if a gate fired: %#v", guidance))

	gomega.Expect(strings.Contains(strings.Join(guidance.ValidNext, "\n"), "gate")).To(gomega.BeFalse(), fmt.Sprintf("search output should not inject gate-specific follow-up guidance: %#v", guidance))
})

var _ = ginkgo.It("build action guidance prefers closure after post child gap blocker", func() {
	guidance := BuildActionGuidance(ActionGuidanceInput{
		Reason:  "pre_execute_guard",
		Request: workspace.ToolRequest{Tool: "read_file", Path: "internal/runtime/agent/runner.go"},
		Result: workspace.ToolResult{
			Tool:    "read_file",
			Summary: "Post-child follow-up missing named gap",
			Error:   "the child packet already contains sufficient evidence to bias toward answering. Before any additional read/search call, include `named_gap` describing one exact unresolved claim this follow-up will verify or falsify. Use `why_not_answering` to state what would be materially wrong or missing if you answered now.",
		},
	})
	gomega.Expect(slices.Contains(guidance.Constraints, "child_packet_available")).To(gomega.BeTrue(), fmt.Sprintf("expected child-packet closure constraint, got %#v", guidance))

	gomega.Expect(slices.Contains(guidance.InvalidNext, "open-ended discovery after readiness without one exact missing claim and one exact probe")).To(gomega.BeFalse(), fmt.Sprintf("answer readiness must not invalidate authorized inspection: %#v", guidance))

	joined := strings.Join(guidance.ValidNext, "\n")
	for _, want := range []string{
		"final answer from the child packet evidence is available",
		"one exact named path, symbol, discovered handle, or narrow range",
		"<<QUESTION>> with the exact blocker",
	} {
		gomega.Expect(strings.Contains(joined, want)).To(gomega.BeTrue(), fmt.Sprintf("expected closure-biased valid action %q, got %#v", want, guidance.ValidNext))
	}

	for _, forbidden := range []string{"search_files", "targeted read-only shell search", "update task_ledger"} {
		gomega.Expect(strings.Contains(joined, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("closure-biased guidance should drop generic fallback %q, got %#v", forbidden, guidance.ValidNext))
	}
})

var _ = ginkgo.It("build recovery plan preserves closure scope on repeated action", func() {
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
		gomega.Expect(strings.Contains(joined, want)).To(gomega.BeTrue(), fmt.Sprintf("expected closure-preserving recovery guidance %q, got %#v", want, plan))
	}

	for _, forbidden := range []string{"search_files", "targeted read-only shell search"} {
		gomega.Expect(strings.Contains(joined, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("repeated-action recovery should not reopen broad fallback %q, got %#v", forbidden, plan))
	}
})

var _ = ginkgo.It("build recovery plan does not promote every valid next to required next", func() {
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
	gomega.Expect(len(plan.RequiredNext) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected repeated-action recovery to keep closure options in valid_next only, got %#v", plan.RequiredNext))

	gomega.Expect(len(plan.ValidNext) == 0).To(gomega.BeFalse(), fmt.Sprintf("expected repeated-action recovery to preserve valid_next options, got %#v", plan))
})

var _ = ginkgo.It("build recovery plan does not treat option list verification as requirement", func() {
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
	gomega.Expect(plan.FailureClass == "verification_unresolved").To(gomega.BeFalse(), fmt.Sprintf("option-list wording should not become verification_unresolved, got %#v", plan))

	gomega.Expect(slices.Contains(plan.RequiredNext, "resolve verification status before claiming completion")).To(gomega.BeFalse(), fmt.Sprintf("option-list wording should not add verification required_next, got %#v", plan))
})

var _ = ginkgo.It("build recovery plan treats failed source sanity as repair obligation", func() {
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
	gomega.Expect(plan.FailureClass != "verification_failed").To(gomega.BeFalse(), fmt.Sprintf("failure class = %q, want verification_failed; plan=%#v", plan.FailureClass, plan))

	joinedRequired := strings.Join(plan.RequiredNext, "\n")
	gomega.Expect(strings.Contains(joinedRequired, "repair the failed verification")).To(gomega.BeTrue(), fmt.Sprintf("expected repair required_next, got %#v", plan.RequiredNext))

	joinedValid := strings.Join(plan.ValidNext, "\n")
	for _, want := range []string{
		"repair the failing changed or required path",
		"one narrow source read or language/context lookup",
		"rerun verification after the repair write",
		"answer with <<QUESTION>> only for an external",
	} {
		gomega.Expect(strings.Contains(joinedValid, want)).To(gomega.BeTrue(), fmt.Sprintf("expected valid_next to contain %q, got %#v", want, plan.ValidNext))
	}

	gomega.Expect(strings.Contains(joinedValid, "answer from the current evidence if it already resolves the task")).To(gomega.BeFalse(), fmt.Sprintf("failed verification should not fall back to generic answer-ready guidance, got %#v", plan.ValidNext))
})

var _ = ginkgo.It("build action guidance closure omits text search fallback after read success", func() {
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
		gomega.Expect(strings.Contains(joined, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("closure guidance should omit %q, got %#v", forbidden, guidance.ValidNext))
	}

	for _, want := range []string{"final answer from the child packet evidence is available", "one exact named path, symbol, discovered handle, or narrow range"} {
		gomega.Expect(strings.Contains(joined, want)).To(gomega.BeTrue(), fmt.Sprintf("expected closure guidance to keep %q, got %#v", want, guidance.ValidNext))
	}
})

var _ = ginkgo.It("build action guidance closure omits outline followup menu", func() {
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
		gomega.Expect(strings.Contains(joined, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("closure guidance should omit %q, got %#v", forbidden, guidance.ValidNext))
	}

	for _, want := range []string{
		"final answer from the child packet evidence is available",
		"one exact named path, symbol, discovered handle, or narrow range",
	} {
		gomega.Expect(strings.Contains(joined, want)).To(gomega.BeTrue(), fmt.Sprintf("expected closure guidance to keep %q, got %#v", want, guidance.ValidNext))
	}
})

var _ = ginkgo.It("build action guidance read success omits edit suggestion for explanation objective", func() {
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

	gomega.Expect(strings.Contains(joined, "edit_file using the inspected evidence")).To(gomega.BeFalse(), fmt.Sprintf("explanatory read guidance should not suggest edits, got %#v", guidance.ValidNext))
})

var _ = ginkgo.It("build action guidance search success omits edit suggestion when read only", func() {
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

	gomega.Expect(strings.Contains(joined, "edit_file if the target is already clear")).To(gomega.BeFalse(), fmt.Sprintf("read-only search guidance should not suggest edits, got %#v", guidance.ValidNext))
})

var _ = ginkgo.It("build action guidance write success uses workspace write catalog", func() {
	guidance := BuildActionGuidance(ActionGuidanceInput{
		Reason:  "tool_result",
		Request: workspace.ToolRequest{Tool: workspace.ToolNameApplyPatch},
		Result:  workspace.ToolResult{Tool: workspace.ToolNameApplyPatch, Summary: "Applied patch with 1 op(s)"},
	})
	joined := strings.Join(guidance.ValidNext, "\n")

	for _, want := range []string{"targeted verification evidence", "required ledger state"} {
		gomega.Expect(strings.Contains(joined, want)).To(gomega.BeTrue(), fmt.Sprintf("expected write-success guidance %q, got %#v", want, guidance.ValidNext))
	}
})

var _ = ginkgo.It("build action guidance search success uses workspace language taxonomy", func() {
	guidance := BuildActionGuidance(ActionGuidanceInput{
		Reason:  "tool_result",
		Request: workspace.ToolRequest{Tool: "ts_references", Path: "src/app.ts", Pattern: "render"},
		Result: workspace.ToolResult{
			Tool:    "ts_references",
			Summary: "Found 3 TypeScript reference(s) for render",
			Output:  "src/app.ts:42 render()\n",
		},
	})
	joined := strings.Join(guidance.ValidNext, "\n")

	gomega.Expect(strings.Contains(joined, "read one exact symbol body, discovered handle, or narrow range from the highest-confidence hit")).To(gomega.BeTrue(), fmt.Sprintf("language search guidance should use workspace taxonomy, got %#v", guidance.ValidNext))
})

var _ = ginkgo.It("build recovery plan classifies failure", func() {
	nativeParseGuidance := BuildActionGuidance(ActionGuidanceInput{
		Reason:            "tool_parse_rejected",
		Request:           workspace.ToolRequest{Tool: "tool_parse"},
		Result:            workspace.ToolResult{Tool: "tool_parse", Summary: "Malformed tool request rejected", Error: "malformed DeepSeek XML tool request"},
		ToolTransportMode: "native",
	})
	joinedNativeGuidance := strings.Join(nativeParseGuidance.ValidNext, "\n")
	gomega.Expect(strings.Contains(joinedNativeGuidance, "active native tool-call transport")).To(gomega.BeTrue(), fmt.Sprintf("native action guidance should preserve native tool transport, got %#v", nativeParseGuidance.ValidNext))
	gomega.Expect(strings.Contains(joinedNativeGuidance, "valid JSON tool object")).To(gomega.BeFalse(), fmt.Sprintf("native action guidance should not ask for textual JSON, got %#v", nativeParseGuidance.ValidNext))

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
			needle: "resolve the completion-gate blocker with a corrected final answer",
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
		ginkgo.By(tc.name)
		func() {
			plan := BuildRecoveryPlan(tc.input)
			gomega.Expect(plan.FailureClass).To(gomega.Equal(tc.want), fmt.Sprintf("FailureClass = %q, want %q in %#v", plan.FailureClass, tc.want, plan))

			joined := strings.Join(append(append([]string{}, plan.DoNotRepeat...), plan.RequiredNext...), "\n")
			gomega.Expect(joined).To(gomega.ContainSubstring(tc.needle), fmt.Sprintf("expected %q in recovery guidance, got %#v", tc.needle, plan))
			if tc.wantNoValidJSON {
				validNext := strings.Join(plan.ValidNext, "\n")
				gomega.Expect(validNext).NotTo(gomega.ContainSubstring("valid JSON tool object"), fmt.Sprintf("native recovery valid_next should not ask for textual JSON, got %#v", plan.ValidNext))
				gomega.Expect(strings.Count(validNext, "active native tool-call transport")).To(gomega.Equal(1), fmt.Sprintf("native recovery valid_next should contain one native transport action, got %#v", plan.ValidNext))
			}
		}()
	}
})

var _ = ginkgo.It("render step instruction includes current state", func() {
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
		gomega.Expect(strings.Contains(rendered, needle)).To(gomega.BeTrue(), fmt.Sprintf("expected %q in rendered instruction:\n%s", needle, rendered))
	}

	gomega.Expect(strings.Count(rendered, "internal/surfaces/tui/app/app.go") != 1).To(gomega.BeFalse(), fmt.Sprintf("expected duplicate paths to be normalized in rendered instruction:\n%s", rendered))
})

var _ = ginkgo.It("render step instruction includes readiness state", func() {
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
		gomega.Expect(strings.Contains(rendered, want)).To(gomega.BeTrue(), fmt.Sprintf("expected readiness field %q in:\n%s", want, rendered))
	}

	gomega.Expect(strings.Count(rendered, "context_packet_available") != 1).To(gomega.BeFalse(), fmt.Sprintf("expected readiness closed claims to be deduplicated:\n%s", rendered))
})

var _ = ginkgo.It("build step instruction prioritizes required edit paths over answer readiness", func() {
	instruction := BuildStepInstruction(RunState{
		Lane:              "implement",
		TaskFamily:        "implementation",
		Permission:        "guarded_workspace_write",
		RequiredEditPaths: []string{"internal/surfaces/tui/app/prompt_routing.go"},
		Readiness: &ReadinessState{
			ReadyToAnswer: true,
		},
	})
	gomega.Expect(strings.Contains(instruction.Directive, "Required edit paths define the implementation scope")).To(gomega.BeTrue(), fmt.Sprintf("expected required-edit-path directive to win, got %q", instruction.Directive))

	gomega.Expect(strings.Contains(instruction.Directive, "answering is supported")).To(gomega.BeFalse(), fmt.Sprintf("answer readiness should not override required edit paths: %q", instruction.Directive))
})

var _ = ginkgo.It("build step instruction prioritizes verification over answer readiness", func() {
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
	gomega.Expect(strings.Contains(instruction.Directive, "verification is the open obligation")).To(gomega.BeTrue(), fmt.Sprintf("expected verification directive to win, got %q", instruction.Directive))

	gomega.Expect(strings.Contains(instruction.Directive, "answering is supported")).To(gomega.BeFalse(), fmt.Sprintf("answer readiness should not override verification: %q", instruction.Directive))
})

var _ = ginkgo.It("build step instruction uses closure directive", func() {
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
	gomega.Expect(strings.Contains(instruction.Directive, "A final answer is available if the evidence supports it")).To(gomega.BeTrue(), fmt.Sprintf("expected closure-biased answer availability directive, got %q", instruction.Directive))

	gomega.Expect(instruction.Directive).To(gomega.ContainSubstring("one exact missing claim"), fmt.Sprintf("expected exact-claim/exact-probe language, got %q", instruction.Directive))
	gomega.Expect(instruction.Directive).To(gomega.ContainSubstring("one exact probe"), fmt.Sprintf("expected exact-claim/exact-probe language, got %q", instruction.Directive))

	gomega.Expect(strings.Contains(instruction.Directive, "options, not mandatory commands")).To(gomega.BeTrue(), fmt.Sprintf("expected closure directive to clarify option-vs-command, got %q", instruction.Directive))
})

var _ = ginkgo.It("build step instruction uses write ready implementation directive", func() {
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
		gomega.Expect(strings.Contains(instruction.Directive, want)).To(gomega.BeTrue(), fmt.Sprintf("expected write-ready directive %q, got %q", want, instruction.Directive))
	}
})

var _ = ginkgo.It("build step instruction adds generic guarded mutation contract after no write gate", func() {
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
		gomega.Expect(strings.Contains(instruction.Directive, want)).To(gomega.BeTrue(), fmt.Sprintf("expected generic guarded mutation directive %q, got %q", want, instruction.Directive))
	}

	gomega.Expect(strings.Contains(instruction.Directive, "answering is supported")).To(gomega.BeFalse(), fmt.Sprintf("generic mutation contract should not be presented as answer-only readiness: %q", instruction.Directive))
})

var _ = ginkgo.It("plan execute allowed scope does not use generic guarded mutation contract", func() {
	initial := BuildInitialStepInstruction(WorkerRuntimeBrief{
		Lane:              "plan-execute",
		TaskFamily:        "plan_then_execute_planner",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedEditPaths:  []string{"pkg/feature.go", "pkg/feature_test.go"},
		AllowedTools:      []string{"context_pack", "read_file", "submit_plan", "edit_file"},
	})
	gomega.Expect(initial.Directive).NotTo(gomega.ContainSubstring("guarded workspace mutation without required_edit_paths"), fmt.Sprintf("plan-execute planner should not receive direct mutation target contract:\n%s", initial.Directive))
	gomega.Expect(initial.Directive).NotTo(gomega.ContainSubstring("write when the exact target"), fmt.Sprintf("plan-execute planner should not receive direct mutation target contract:\n%s", initial.Directive))

	step := BuildStepInstruction(RunState{
		Lane:             "plan-execute",
		TaskFamily:       "plan_then_execute_planner",
		Contract:         "implementation_result",
		Permission:       "guarded_workspace_write",
		AllowedEditPaths: []string{"pkg/feature.go", "pkg/feature_test.go"},
		Readiness:        &ReadinessState{ReadyToAnswer: true},
	})
	gomega.Expect(step.Directive).NotTo(gomega.ContainSubstring("guarded workspace mutation without required_edit_paths"), fmt.Sprintf("plan-execute planner step should not receive direct mutation target contract:\n%s", step.Directive))
	gomega.Expect(step.Directive).NotTo(gomega.ContainSubstring("write when the exact target"), fmt.Sprintf("plan-execute planner step should not receive direct mutation target contract:\n%s", step.Directive))
})

var _ = ginkgo.It("build step instruction only uses remaining path directive for missing paths", func() {
	partial := BuildStepInstruction(RunState{
		Lane:              "implement",
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		Permission:        "guarded_workspace_write",
		RequiredEditPaths: []string{"a.txt", "b.txt"},
		ChangedPaths:      []string{"a.txt"},
	})
	gomega.Expect(strings.Contains(partial.Directive, "Required edit paths remain contractual")).To(gomega.BeTrue(), fmt.Sprintf("expected remaining-path directive for partial edit, got %q", partial.Directive))

	complete := BuildStepInstruction(RunState{
		Lane:              "implement",
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		Permission:        "guarded_workspace_write",
		RequiredEditPaths: []string{"a.txt", "b.txt"},
		ChangedPaths:      []string{"a.txt", "b.txt"},
	})
	gomega.Expect(strings.Contains(complete.Directive, "Required edit paths remain contractual")).To(gomega.BeFalse(), fmt.Sprintf("did not expect remaining-path directive once all required paths changed, got %q", complete.Directive))
})

var _ = ginkgo.It("render initial runtime brief includes policy manifest and targets", func() {
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
		gomega.Expect(strings.Contains(rendered, needle)).To(gomega.BeTrue(), fmt.Sprintf("expected %q in initial runtime brief:\n%s", needle, rendered))
	}
})

var _ = ginkgo.It("render initial runtime brief adds guarded write first action contract", func() {
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
		gomega.Expect(strings.Contains(rendered, want)).To(gomega.BeTrue(), fmt.Sprintf("expected guarded-write first-action directive %q in:\n%s", want, rendered))
	}

	readOnly := RenderInitialRuntimeBrief(WorkerRuntimeBrief{
		Lane:              "architecture",
		TaskFamily:        "architecture",
		Contract:          "architecture_assessment",
		PermissionProfile: "read_only_workspace",
		AllowedTools:      []string{"search_content", "read_file"},
	})
	gomega.Expect(strings.Contains(readOnly, "first assistant response must be either exactly one tool call")).To(gomega.BeFalse(), fmt.Sprintf("read-only runtime brief should not include guarded-write first-action directive:\n%s", readOnly))
})

var _ = ginkgo.It("render initial runtime brief adds generic guarded mutation contract without required paths", func() {
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
		gomega.Expect(strings.Contains(rendered, want)).To(gomega.BeTrue(), fmt.Sprintf("expected generic guarded mutation contract %q in:\n%s", want, rendered))
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
		gomega.Expect(strings.Contains(rendered, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("generic guarded mutation contract should not include %q:\n%s", forbidden, rendered))
	}
})

var _ = ginkgo.It("render initial runtime brief adds required edit path readiness directive", func() {
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
		gomega.Expect(strings.Contains(rendered, want)).To(gomega.BeTrue(), fmt.Sprintf("expected required-path readiness directive %q in:\n%s", want, rendered))
	}
})

var _ = ginkgo.It("render initial runtime brief uses plan execute submit plan directive", func() {
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
		gomega.Expect(strings.Contains(rendered, want)).To(gomega.BeTrue(), fmt.Sprintf("plan-execute runtime brief missing %q:\n%s", want, rendered))
	}

	gomega.Expect(strings.Contains(rendered, "in-scope required-path edit")).To(gomega.BeFalse(), fmt.Sprintf("plan-execute runtime brief must not use implementation edit wording:\n%s", rendered))
})

var _ = ginkgo.It("render initial runtime brief adds deterministic discovery examples", func() {
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
		gomega.Expect(strings.Contains(rendered, needle)).To(gomega.BeTrue(), fmt.Sprintf("expected %q in deterministic discovery guidance:\n%s", needle, rendered))
	}

	gomega.Expect(rendered).NotTo(gomega.ContainSubstring("architecture_boundary shape"), fmt.Sprintf("runtime brief should not repeat verbose discovery_query examples:\n%s", rendered))
	gomega.Expect(rendered).NotTo(gomega.ContainSubstring("security_review:"), fmt.Sprintf("runtime brief should not repeat verbose discovery_query examples:\n%s", rendered))

	for _, forbidden := range []string{
		"use one bounded discovery_query",
		"run context_pack",
		"call go_definition",
		"invoke read_only_capability",
	} {
		gomega.Expect(strings.Contains(strings.ToLower(rendered), strings.ToLower(forbidden))).To(gomega.BeFalse(), fmt.Sprintf("runtime brief should avoid prescriptive raw-tool phrase %q:\n%s", forbidden, rendered))
	}
})

var _ = ginkgo.It("normalize runtime brief summarizes capabilities without language adapter list", func() {
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

	gomega.Expect(len(brief.EvidenceCapabilities) == 0).To(gomega.BeFalse(), fmt.Sprintf("expected compact capability summary, got %#v", brief))

	joined := strings.Join(brief.EvidenceCapabilities, "\n")
	for _, want := range []string{"inspect_file", "inspect_symbol", "find_definition", "find_references", workspace.CapabilityClassCompatibilityAdapter} {
		gomega.Expect(strings.Contains(joined, want)).To(gomega.BeTrue(), fmt.Sprintf("capability summary missing %q:\n%s", want, joined))
	}

	for _, verboseAdapter := range []string{"go_definition", "go_references", "python_read_symbol", "language adapters", "tools:"} {
		gomega.Expect(strings.Contains(joined, verboseAdapter)).To(gomega.BeFalse(), fmt.Sprintf("capability summary should collapse language adapter %q:\n%s", verboseAdapter, joined))
	}
})

var _ = ginkgo.It("render initial runtime brief suppresses discovery examples for write ready packet", func() {
	rendered := RenderInitialRuntimeBrief(WorkerRuntimeBrief{
		Lane:              "implement",
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"read_file", "edit_file", "discovery_query"},
		RequiredEditPaths: []string{"native/tests/test_energy.cpp"},
		TargetPacket:      []string{"[implementation target packet]", "verified anchor: native/tests/test_energy.cpp"},
	})
	gomega.Expect(rendered).NotTo(gomega.ContainSubstring(`"discovery_examples"`), fmt.Sprintf("write-ready implementation packet should not advertise discovery examples:\n%s", rendered))
	gomega.Expect(rendered).NotTo(gomega.ContainSubstring("use one bounded discovery_query"), fmt.Sprintf("write-ready implementation packet should not advertise discovery examples:\n%s", rendered))

	gomega.Expect(strings.Contains(rendered, "required_edit_paths are present")).To(gomega.BeTrue(), fmt.Sprintf("expected write-ready required-path directive to remain:\n%s", rendered))
})

var _ = ginkgo.It("render initial runtime brief omits discovery examples without tool access", func() {
	rendered := RenderInitialRuntimeBrief(WorkerRuntimeBrief{
		Lane:              "main",
		TaskFamily:        "direct_question",
		Contract:          "direct_answer",
		PermissionProfile: "conversation_only",
		AllowedTools:      []string{"discovery_query"},
	})
	gomega.Expect(rendered).NotTo(gomega.ContainSubstring("discovery_examples"), fmt.Sprintf("conversation-only runtime brief should not advertise discovery examples:\n%s", rendered))
	gomega.Expect(rendered).NotTo(gomega.ContainSubstring("purpose\":\"impact_analysis"), fmt.Sprintf("conversation-only runtime brief should not advertise discovery examples:\n%s", rendered))
})

var _ = ginkgo.It("build initial step instruction conversation names default read only answer lane", func() {
	step := BuildInitialStepInstruction(WorkerRuntimeBrief{
		Lane:              "main",
		TaskFamily:        "conversation",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		AllowedTools:      []string{"search_content", "read_file", "context_query"},
	})
	gomega.Expect(step.Directive).To(gomega.ContainSubstring("Conversation lane"), fmt.Sprintf("conversation initial directive should explain the default read-only answer contract:\n%s", step.Directive))
	gomega.Expect(step.Directive).To(gomega.ContainSubstring("every response must either"), fmt.Sprintf("conversation initial directive should explain the default read-only answer contract:\n%s", step.Directive))
	gomega.Expect(step.Directive).To(gomega.ContainSubstring("Choose from the available evidence capabilities according to the specific gap"), fmt.Sprintf("conversation initial directive should explain the default read-only answer contract:\n%s", step.Directive))
	gomega.Expect(step.Directive).To(gomega.ContainSubstring("Do not return raw tool summaries"), fmt.Sprintf("conversation initial directive should explain the default read-only answer contract:\n%s", step.Directive))

	for _, forbidden := range []string{"10-35", "prefer this order", "Use context_pack to localize", "Use the note tool"} {
		gomega.Expect(strings.Contains(step.Directive, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("conversation initial directive should not prescribe strategy cue %q:\n%s", forbidden, step.Directive))
	}

	for _, want := range []string{
		"Evidence contract / exit criteria are summarized in current_state.evidence_contract",
		"localization/context packets scope candidates but do not complete evidence by themselves",
		"source, symbol, narrow line-range, read_symbol/extract_body, or routed source-body evidence",
		"answer once inspected evidence resolves the question",
		"one exact unresolved claim and one bounded probe",
	} {
		gomega.Expect(strings.Contains(step.Directive+"\n"+strings.Join(step.CurrentState.EvidenceContract, "\n"), want)).To(gomega.BeTrue(), fmt.Sprintf("conversation initial evidence contract should include %q:\ndirective=%s\ncontract=%#v", want, step.Directive, step.CurrentState.EvidenceContract))
	}

	gomega.Expect(strings.Contains(step.Directive, "The readiness state says answering is supported")).To(gomega.BeFalse(), fmt.Sprintf("conversation initial directive must not reintroduce premature readiness wording:\n%s", step.Directive))
})

var _ = ginkgo.It("render initial runtime brief conversation renders evidence contract before first action", func() {
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
		gomega.Expect(strings.Contains(rendered, want)).To(gomega.BeTrue(), fmt.Sprintf("rendered initial conversation brief should include %q:\n%s", want, rendered))
	}

	gomega.Expect(strings.Contains(rendered, "The readiness state says answering is supported")).To(gomega.BeFalse(), fmt.Sprintf("rendered initial conversation brief must not claim answer readiness:\n%s", rendered))
})

var _ = ginkgo.It("build initial step instruction requires evidence tool when read only gate open", func() {
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
		gomega.Expect(strings.Contains(step.Directive, want)).To(gomega.BeTrue(), fmt.Sprintf("expected %q in read-only evidence-pending directive:\n%s", want, step.Directive))
	}
})

var _ = ginkgo.It("initial action contract applies uses exact mutation contracts", func() {
	gomega.Expect(initialActionContractApplies(RunState{TaskFamily: "implementation_notes", Contract: "direct_answer", Permission: "read_only_workspace"})).To(gomega.BeFalse(), fmt.Sprint("read-only implementation-adjacent wording must not activate a mutation contract"))

	gomega.Expect(initialActionContractApplies(RunState{TaskFamily: "implementation", Contract: "implementation_result", Permission: "guarded_workspace_write"})).To(gomega.BeTrue(), fmt.Sprint("exact implementation contract should activate mutation contract"))
})

var _ = ginkgo.It("normalize runtime brief conversation only clears tool surface", func() {
	brief := NormalizeRuntimeBrief(WorkerRuntimeBrief{
		PermissionProfile: "conversation_only",
		AllowedTools:      []string{"task", "search_files"},
		RequiredTools:     []string{"search_files"},
		DiscoveryExamples: []string{"discovery-packet capability available"},
	})
	gomega.Expect(len(brief.AllowedTools) != 0).To(gomega.BeFalse(), fmt.Sprintf("conversation-only brief should not advertise tools, got %#v", brief.AllowedTools))

	gomega.Expect(len(brief.RequiredTools) != 0).To(gomega.BeFalse(), fmt.Sprintf("conversation-only brief should not require tools, got %#v", brief.RequiredTools))

	gomega.Expect(len(brief.ToolManifest) != 0).To(gomega.BeFalse(), fmt.Sprintf("conversation-only brief should not emit a tool manifest, got %#v", brief.ToolManifest))

	gomega.Expect(len(brief.DiscoveryExamples) != 0).To(gomega.BeFalse(), fmt.Sprintf("conversation-only brief should not emit discovery examples, got %#v", brief.DiscoveryExamples))
})

var _ = ginkgo.It("normalize runtime brief read only workspace suppresses write tools", func() {
	brief := NormalizeRuntimeBrief(WorkerRuntimeBrief{
		TaskFamily:        "implementation_notes",
		Contract:          "direct_answer",
		PermissionProfile: "read_only_workspace",
		AllowedTools:      []string{"read_file", "edit_file", "apply_patch", "submit_plan", "search_content"},
		ForbiddenTools:    []string{"mutating_shell"},
		RequiredTools:     []string{workspace.RequiredToolEditFileOrApplyPatch, "workspace_inspection"},
	})

	for _, forbidden := range []string{"edit_file", "apply_patch", "submit_plan"} {
		gomega.Expect(slices.Contains(brief.AllowedTools, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("read-only workspace brief should not advertise %q in %#v", forbidden, brief.AllowedTools))
		gomega.Expect(runtimeTestToolManifestContains(brief.ToolManifest, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("read-only workspace brief should not manifest %q in %#v", forbidden, brief.ToolManifest))
		gomega.Expect(slices.Contains(brief.ForbiddenTools, forbidden)).To(gomega.BeTrue(), fmt.Sprintf("read-only workspace brief should forbid %q in %#v", forbidden, brief.ForbiddenTools))
	}

	gomega.Expect(brief.AllowedTools).To(gomega.ContainElement("read_file"), fmt.Sprintf("read-only workspace brief lost read tools: %#v", brief.AllowedTools))
	gomega.Expect(brief.AllowedTools).To(gomega.ContainElement("search_content"), fmt.Sprintf("read-only workspace brief lost read tools: %#v", brief.AllowedTools))
	gomega.Expect(slices.Contains(brief.RequiredTools, workspace.RequiredToolEditFileOrApplyPatch)).To(gomega.BeFalse(), fmt.Sprintf("read-only workspace brief should not require edit tools: %#v", brief.RequiredTools))
	gomega.Expect(slices.Contains(brief.RequiredTools, "workspace_inspection")).To(gomega.BeTrue(), fmt.Sprintf("read-only workspace brief lost read-only required tool: %#v", brief.RequiredTools))
})

func runtimeTestToolManifestContains(manifest []workspace.ToolManifestEntry, tool string) bool {
	for _, entry := range manifest {
		if strings.TrimSpace(entry.Tool) == tool {
			return true
		}
	}

	return false
}

var _ = ginkgo.It("normalize runtime brief suppresses placeholder manifest requests", func() {
	brief := NormalizeRuntimeBrief(WorkerRuntimeBrief{
		AllowedTools: []string{"submit_plan", "pwd"},
		ToolManifest: []workspace.ToolManifestEntry{
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

	byTool := map[string]workspace.ToolManifestEntry{}
	for _, entry := range brief.ToolManifest {
		byTool[entry.Tool] = entry
	}

	if got := byTool["submit_plan"].Request; got != "" {
		ginkgo.Fail(fmt.Sprintf("placeholder submit_plan request should be suppressed, got %q", got))
	}

	if got := byTool["pwd"].Request; got == "" {
		ginkgo.Fail(fmt.Sprint("concrete request should be preserved"))
	}
})

var _ = ginkgo.It("normalize runtime brief builds workspace owned tool manifest", func() {
	brief := NormalizeRuntimeBrief(WorkerRuntimeBrief{AllowedTools: []string{"submit_plan", "pwd"}})

	byTool := map[string]workspace.ToolManifestEntry{}
	for _, entry := range brief.ToolManifest {
		byTool[entry.Tool] = entry
	}

	if got := byTool["submit_plan"].Request; got != "" {
		ginkgo.Fail(fmt.Sprintf("workspace placeholder request should be suppressed at runtime boundary, got %q", got))
	}

	if got := byTool["pwd"].Request; got == "" {
		ginkgo.Fail(fmt.Sprint("concrete request should remain available"))
	}
})

var _ = ginkgo.It("role spec for child write carries isolated contract", func() {
	spec := RoleSpecFor("workbranch_write")
	gomega.Expect(spec.Name).To(gomega.Equal(RoleChildWrite), fmt.Sprintf("unexpected child write role: %#v", spec))
	gomega.Expect(spec.PermissionProfile).To(gomega.Equal(PermissionIsolatedWorkbranchWrite), fmt.Sprintf("unexpected child write role: %#v", spec))

	for _, want := range []string{"edit_file", "apply_patch", "go_move_declarations", "shell"} {
		gomega.Expect(slices.Contains(spec.AllowedTools, want)).To(gomega.BeTrue(), fmt.Sprintf("expected child write role to allow %q, got %#v", want, spec.AllowedTools))
	}

	for _, want := range []string{"task", "git_history_mutation", "git_index_mutation", "git_push", "publish", "deploy"} {
		gomega.Expect(slices.Contains(spec.ForbiddenTools, want)).To(gomega.BeTrue(), fmt.Sprintf("expected child write role to forbid %q, got %#v", want, spec.ForbiddenTools))
	}

	brief := RuntimeBriefForRole("child_write", "child_write:patch", "patch README")
	gomega.Expect(brief.Lane).To(gomega.Equal("child_write:patch"), fmt.Sprintf("expected normalized child write runtime brief, got %#v", brief))
	gomega.Expect(brief.ToolManifest).NotTo(gomega.BeNil(), fmt.Sprintf("expected normalized child write runtime brief, got %#v", brief))
})

var _ = ginkgo.It("role spec for child read only omits nested task delegation", func() {
	spec := RoleSpecFor("child_read_only")
	gomega.Expect(spec.Name).To(gomega.Equal(RoleChildReadOnly), fmt.Sprintf("unexpected child read-only role: %#v", spec))
	gomega.Expect(spec.PermissionProfile).To(gomega.Equal(PermissionReadOnlyWorkspace), fmt.Sprintf("unexpected child read-only role: %#v", spec))

	for _, forbidden := range []string{"task", "list_files"} {
		gomega.Expect(slices.Contains(spec.AllowedTools, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("child read-only role must not advertise %q, got %#v", forbidden, spec.AllowedTools))
	}

	for _, want := range []string{"read_file", "search_files", "search_content", "file_stats", "code_navigate", "read_only_capability", "context_pack", "discovery_query", "structural_search"} {
		gomega.Expect(slices.Contains(spec.AllowedTools, want)).To(gomega.BeTrue(), fmt.Sprintf("expected child read-only role to allow %q, got %#v", want, spec.AllowedTools))
	}

	for _, want := range []string{"answer_when_evidence_is_sufficient", "if_not_answering_name_one_exact_missing_claim_and_probe", "prefer_symbol_or_handle_probes_over_raw_path_reads"} {
		gomega.Expect(runtimeLabelContains(spec.SearchPolicy, want)).To(gomega.BeTrue(), fmt.Sprintf("expected child read-only search policy to include %q, got %#v", want, spec.SearchPolicy))
	}

	for _, tool := range workspace.WorkspaceWriteToolNames() {
		gomega.Expect(slices.Contains(spec.ForbiddenTools, tool)).To(gomega.BeTrue(), fmt.Sprintf("child read-only role must forbid cataloged workspace write tool %q, got %#v", tool, spec.ForbiddenTools))
	}
})

var _ = ginkgo.It("role spec for repair carries repair contract", func() {
	spec := RoleSpecFor("repair")
	contract := RuntimeContractForTaskFamily(string(autonomy.TaskFamilyImplementationRepair))
	gomega.Expect(spec.Name).To(gomega.Equal(RoleRepair), fmt.Sprintf("unexpected repair role: %#v", spec))
	gomega.Expect(spec.TaskFamily).To(gomega.Equal(contract.TaskFamily), fmt.Sprintf("unexpected repair role: %#v", spec))
	gomega.Expect(spec.Contract).To(gomega.Equal(contract.Contract), fmt.Sprintf("unexpected repair role: %#v", spec))

	gomega.Expect(spec.PermissionProfile).To(gomega.Equal(contract.PermissionProfile), fmt.Sprintf("repair role drifted from runtime contract: %#v / %#v", spec, contract))
	gomega.Expect(spec.VerificationContract).To(gomega.Equal(contract.VerificationContract), fmt.Sprintf("repair role drifted from runtime contract: %#v / %#v", spec, contract))

	gomega.Expect(slices.Equal(spec.SearchPolicy, contract.SearchPolicy)).To(gomega.BeTrue(), fmt.Sprintf("repair role should copy catalog-owned contract fields:\nrole=%#v\ncontract=%#v", spec, contract))
	gomega.Expect(slices.Equal(spec.RequiredEvidence, contract.RequiredEvidence)).To(gomega.BeTrue(), fmt.Sprintf("repair role should copy catalog-owned contract fields:\nrole=%#v\ncontract=%#v", spec, contract))
	gomega.Expect(slices.Equal(spec.ReadinessGate, contract.ReadinessGate)).To(gomega.BeTrue(), fmt.Sprintf("repair role should copy catalog-owned contract fields:\nrole=%#v\ncontract=%#v", spec, contract))
	gomega.Expect(slices.Equal(spec.StopConditions, contract.StopConditions)).To(gomega.BeTrue(), fmt.Sprintf("repair role should copy catalog-owned contract fields:\nrole=%#v\ncontract=%#v", spec, contract))

	for _, want := range []string{"unbounded_write", "unapproved_protected_write"} {
		gomega.Expect(slices.Contains(spec.ForbiddenTools, want)).To(gomega.BeTrue(), fmt.Sprintf("repair role should keep role-specific forbidden tool %q, got %#v", want, spec.ForbiddenTools))
	}
})

var _ = ginkgo.It("role spec for implement trusts preloaded anchors", func() {
	spec := RoleSpecFor("implement")
	for _, want := range []string{"consume_target_packet_first", "trust_preloaded_current_state_anchors", "write_once_edit_location_is_supported"} {
		gomega.Expect(runtimeLabelContains(spec.SearchPolicy, want)).To(gomega.BeTrue(), fmt.Sprintf("expected implement search policy to include %q, got %#v", want, spec.SearchPolicy))
	}
})

var _ = ginkgo.It("role spec for main uses conversation only fallback", func() {
	spec := RoleSpecFor("main")
	gomega.Expect(spec.Name).To(gomega.Equal(RoleMain), fmt.Sprintf("unexpected main role: %#v", spec))
	gomega.Expect(spec.PermissionProfile).To(gomega.Equal(PermissionConversationOnly), fmt.Sprintf("unexpected main role: %#v", spec))

	gomega.Expect(len(spec.AllowedTools) != 0).To(gomega.BeFalse(), fmt.Sprintf("main role should not advertise tools by default, got %#v", spec.AllowedTools))

	for _, tool := range workspace.WorkspaceWriteToolNames() {
		gomega.Expect(slices.Contains(spec.ForbiddenTools, tool)).To(gomega.BeTrue(), fmt.Sprintf("main role must forbid cataloged workspace write tool %q, got %#v", tool, spec.ForbiddenTools))
	}
})
