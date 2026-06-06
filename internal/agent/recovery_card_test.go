package agent

import (
	"slices"
	"strings"
	"testing"

	"idiom/internal/frontier/workerruntime"
	"idiom/internal/workspace"
)

func TestRecoveryCardRendersZeroHit(t *testing.T) {
	got := RenderRecoveryCard(RecoveryCard{
		ContractID:  "contract-7",
		PhaseID:     turnPhaseCandidateSource,
		InvariantID: "evidence.zero_hit_not_absence",
		EvidenceRefs: []string{
			"tool_result:search_content:step_3",
			"tool_result:search_content:step_3",
		},
		RejectedAction: RecoveryRejectedAction{
			Tool:        "search_content",
			ActionClass: actionSearch,
			Target:      "MissingSymbol",
			Reason:      "zero hits",
		},
		Invariant: "A zero-hit search is not source-body evidence for absence.",
		InvalidRepeats: []string{
			"repeat the same zero-hit search",
			"broaden the query without naming one missing claim",
		},
		CallableTools:               []string{"context_pack", "read_file", "code_navigate", "search_content"},
		CallableActionClasses:       []string{actionRead, actionSearch, actionBlocker},
		DeterministicSafeNextAction: "Use a bounded source read only when a candidate path is known.",
		ExactBlockerShape:           "<<QUESTION>>: Cannot verify <claim> because the search returned zero candidate paths and no source anchor is available.",
	})

	assertRecoveryCardContains(t, got,
		"Recovery card",
		"schema: idiom.recovery_card.v1",
		"contract_id=contract-7",
		"phase_id=plan_execute.candidate_source",
		"invariant_id=evidence.zero_hit_not_absence",
		"evidence: tool_result:search_content:step_3",
		"rejected action: tool=search_content action_class=search target=MissingSymbol reason=\"zero hits\"",
		"invariant: evidence.zero_hit_not_absence: A zero-hit search is not source-body evidence for absence.",
		"invalid repeats: repeat the same zero-hit search, broaden the query without naming one missing claim",
		"currently callable tools: context_pack, read_file, code_navigate, search_content",
		"currently callable action classes: read, search, blocker",
		"deterministic safe next action: Use a bounded source read only when a candidate path is known.",
		"exact blocker shape: <<QUESTION>>: State the exact blocker and cite the concrete evidence source, path, command, or capability that is unavailable or failed.",
	)
	assertRecoveryCardOmitsProjectWorkflowTerms(t, got)
	if strings.Count(got, "tool_result:search_content:step_3") != 1 {
		t.Fatalf("expected duplicate evidence refs to be collapsed, got:\n%s", got)
	}
}

func TestRecoveryCardRendersRepeatedReadDeadEnd(t *testing.T) {
	got := RenderRecoveryCard(RecoveryCard{
		PhaseID:     turnPhaseRequiredSource,
		InvariantID: "loop.repeated_dead_end",
		EvidenceRefs: []string{
			"tool_result:read_file:step_5",
			"tool_result:read_file:step_6",
		},
		RejectedAction: RecoveryRejectedAction{
			Tool:        "read_file",
			ActionClass: actionRead,
			Target:      "internal/example.go",
			Reason:      "same source body already read",
		},
		Invariant:                   "Repeated reads of the same evidence do not close a new claim.",
		InvalidRepeats:              []string{"read the same path and range again", "ask for another generic probe without a named gap"},
		CallableTools:               []string{"read_file", "code_navigate"},
		CallableActionClasses:       []string{actionRead, actionBlocker},
		DeterministicSafeNextAction: "Name one exact missing claim and inspect only the source range that can resolve it.",
		ExactBlockerShape:           "<<QUESTION>>: Cannot resolve <claim> because <specific source range or capability> is unavailable after the repeated read.",
	})

	assertRecoveryCardContains(t, got,
		"phase_id=plan_execute.required_source",
		"invariant_id=loop.repeated_dead_end",
		"rejected action: tool=read_file action_class=read target=internal/example.go reason=\"same source body already read\"",
		"invalid repeats: read the same path and range again, ask for another generic probe without a named gap",
		"currently callable tools: read_file, code_navigate",
		"currently callable action classes: read, blocker",
		"deterministic safe next action: Name one exact missing claim and inspect only the source range that can resolve it.",
		"exact blocker shape: <<QUESTION>>: State the exact blocker and cite the concrete evidence source, path, command, or capability that is unavailable or failed.",
	)
	assertRecoveryCardOmitsProjectWorkflowTerms(t, got)
}

func TestRecoveryCardRendersCompletionGateStyleCard(t *testing.T) {
	got := RenderRecoveryCard(RecoveryCard{
		ContractID:  "completion-gate",
		PhaseID:     turnPhaseImplementationVerifyReady,
		InvariantID: "completion.post_write_verification",
		EvidenceRefs: []string{
			"changed_path:internal/example.go",
			"verification:none_after_write",
		},
		RejectedAction: RecoveryRejectedAction{
			ActionClass: actionFinalAnswer,
			Reason:      "final answer before verification evidence",
		},
		Invariant:             "A completion answer requires verification evidence or an exact verification blocker after writes.",
		InvalidRepeats:        []string{"final answer without verification status", "more source reads before naming the verification blocker"},
		CallableActionClasses: []string{actionVerify, actionBlocker},
		ExactBlockerShape:     "<<QUESTION>>: Verification blocked because <exact command or capability> cannot run: <specific reason>.",
	})

	assertRecoveryCardContains(t, got,
		"contract_id=completion-gate",
		"phase_id=implementation.verify_ready",
		"invariant_id=completion.post_write_verification",
		"evidence: changed_path:internal/example.go, verification:none_after_write",
		"rejected action: action_class=final_answer reason=\"final answer before verification evidence\"",
		"currently callable tools: none",
		"currently callable action classes: verify, blocker",
		"deterministic safe next action: none available",
		"exact blocker shape: <<QUESTION>>: State the exact blocker and cite the concrete evidence source, path, command, or capability that is unavailable or failed.",
	)
	assertRecoveryCardOmitsProjectWorkflowTerms(t, got)
}

func TestRecoveryCardDoesNotAdvertiseCompletionGateAsCallableTool(t *testing.T) {
	got := RenderRecoveryCard(RecoveryCard{
		RejectedAction: RecoveryRejectedAction{
			Tool:   "completion_gate",
			Reason: "unsupported detail",
		},
		CallableTools:         []string{"read_file", "code_navigate"},
		CallableActionClasses: []string{actionFinalAnswer, actionRead, actionBlocker},
	})

	assertRecoveryCardContains(t, got,
		"rejected action: action_class=final_answer reason=\"unsupported detail\"",
		"currently callable tools: read_file, code_navigate",
	)
	if strings.Contains(got, "tool=completion_gate") {
		t.Fatalf("model-facing recovery card must not advertise internal completion_gate as a tool:\n%s", got)
	}
}

func TestBuildRecoveryCardDerivesCallableSurfaceFromPhaseContract(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implement",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"a.go"},
			AllowedTools:      []string{"read_file", "source_sanity", "shell", "apply_patch"},
		},
		Results: []workspace.ToolResult{
			{Tool: "apply_patch", Summary: "Patched files", Output: "update a.go\n"},
		},
		BaseAllowedTools: []string{"read_file", "source_sanity", "shell", "apply_patch"},
	})
	card := BuildRecoveryCard(RecoveryCardInput{
		PhaseContract:       contract,
		CurrentAllowedTools: []string{"read_file", "source_sanity", "shell", "apply_patch"},
		RejectedRequest:     workspace.ToolRequest{Tool: "completion_gate"},
		RejectedResult: workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Completion gate rejected final answer",
			Error:   "post-write verification is unresolved",
		},
	})
	if contract.PhaseID != turnPhaseImplementationVerifyReady {
		t.Fatalf("PhaseID = %q, want %q", contract.PhaseID, turnPhaseImplementationVerifyReady)
	}
	for _, class := range contract.RuntimeCall.ExpressibleActionClasses {
		if !slices.Contains(card.CallableActionClasses, class) {
			t.Fatalf("recovery card callable action classes %#v missing contract class %q", card.CallableActionClasses, class)
		}
	}
	if slices.Contains(card.CallableTools, "read_file") || slices.Contains(card.CallableTools, "apply_patch") {
		t.Fatalf("verification recovery card should use current phase callable tools, got %#v", card.CallableTools)
	}
	for _, want := range []string{"source_sanity", "shell"} {
		if !slices.Contains(card.CallableTools, want) {
			t.Fatalf("verification recovery card missing callable tool %q: %#v", want, card.CallableTools)
		}
	}
	assertRecoveryCardOmitsProjectWorkflowTerms(t, RenderRecoveryCard(card))
}

func TestBuildRecoveryCardDoesNotAdvertiseBlockerWhenSubmitCorrectionRequiresPlanOnly(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "code_navigate", "submit_plan"},
			RequiredEditPaths: []string{"a.go"},
		},
		Results: []workspace.ToolResult{
			{Tool: "read_file", Summary: "Read f1 a.go lines 1-10 (total lines: 10)"},
			{
				Tool:    "submit_plan",
				Summary: "submit_plan rejected: required edit path coverage mismatch",
				Error:   "missing required edit path(s): `a.go`.",
			},
		},
		BaseAllowedTools:  []string{"read_file", "code_navigate", "submit_plan"},
		TextualToolParser: true,
		ToolTransportMode: "textual",
		ResponseFormat:    "json",
	})
	card := BuildRecoveryCard(RecoveryCardInput{
		PhaseContract:       contract,
		CurrentAllowedTools: []string{"submit_plan"},
		RejectedRequest:     workspace.ToolRequest{Tool: "submit_plan"},
		RejectedResult: workspace.ToolResult{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "missing required edit path(s): `a.go`.",
		},
		ExactBlockerShape: "<<QUESTION>>: stale blocker escape hatch",
	})
	got := RenderRecoveryCard(card)

	assertRecoveryCardContains(t, got,
		"invariant: parser.submit_plan_required: The active phase requires one parser-readable submit_plan action that satisfies the current phase contract.",
		"currently callable action classes: submit_plan",
		"deterministic safe next action: Emit one parser-readable planner outcome: corrected submit_plan",
		"exact blocker shape: none available",
	)
	if strings.Contains(got, "supported terminal blocker") ||
		strings.Contains(got, "stale blocker escape hatch") ||
		strings.Contains(got, "currently callable action classes: submit_plan, blocker") {
		t.Fatalf("submit-correction recovery card advertised blocker despite submit_plan-only contract:\n%s", got)
	}
}

func TestRecoveryCardForTextualReadOnlyEvidencePendingKeepsReadTools(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			Objective:         "Compare internal/labels/labels.go and internal/labels/labels_test.go.",
			AllowedTools:      []string{"read_file", "search_content", "code_navigate"},
		},
		Results: []workspace.ToolResult{
			{Tool: "read_file", Summary: "Read f1 internal/labels/labels.go lines 1-5"},
			{Tool: "code_navigate", Summary: "Code navigation routed references via go_references"},
		},
		BaseAllowedTools:  []string{"read_file", "search_content", "code_navigate"},
		TextualToolParser: true,
		ToolTransportMode: "textual",
	})
	if contract.Phase != "read_only_evidence_pending" {
		t.Fatalf("Phase = %q, want read_only_evidence_pending", contract.Phase)
	}
	card := BuildRecoveryCard(RecoveryCardInput{
		PhaseContract:       contract,
		CurrentAllowedTools: []string{"read_file", "search_content", "code_navigate"},
		RejectedRequest:     workspace.ToolRequest{Tool: "code_navigate", Path: "internal/app"},
		RejectedResult: workspace.ToolResult{
			Tool:    "code_navigate",
			Summary: "Repeated dead-end action rejected",
			Error:   "this action class already failed earlier in the turn",
		},
	})
	got := RenderRecoveryCard(card)
	assertRecoveryCardContains(t, got,
		"currently callable tools: read_file, search_content, code_navigate",
		"currently callable action classes:",
		"read",
		"search",
	)
	if strings.Contains(got, "currently callable tools: none") {
		t.Fatalf("read-only pending recovery card must not close the textual tool surface:\n%s", got)
	}
	assertRecoveryCardOmitsProjectWorkflowTerms(t, got)
}

func TestRecoveryCardOmitsHiddenToolsFromCallableSurface(t *testing.T) {
	contract := TurnPhaseContract{
		Phase:        "generic",
		PhaseID:      turnPhaseGeneric,
		AllowedTools: []string{"task", "read_file", "code_navigate"},
		HiddenTools:  []string{"task"},
		ActionPolicy: TurnActionClassPolicy{
			AllowedActionClasses: []string{actionRead, actionGenericProbe, actionFinalAnswer, actionBlocker},
			CallableToolNames:    []string{"task", "read_file", "code_navigate"},
		},
		RuntimeCall: RuntimeCallContract{
			ExpressibleActionClasses: []string{actionRead, actionGenericProbe, actionFinalAnswer, actionBlocker},
		},
	}

	card := BuildRecoveryCard(RecoveryCardInput{
		PhaseContract:       contract,
		CurrentAllowedTools: []string{"task", "read_file", "code_navigate"},
		RejectedRequest:     workspace.ToolRequest{Tool: "task", Agent: "explorer"},
		RejectedResult: workspace.ToolResult{
			Tool:    "task",
			Summary: "Tool hidden by active contract",
			Error:   "task is hidden for the rest of this turn by the active runtime contract.",
		},
	})

	if slices.Contains(card.CallableTools, "task") {
		t.Fatalf("hidden task must not be advertised as callable: %#v", card.CallableTools)
	}
	for _, want := range []string{"read_file", "code_navigate"} {
		if !slices.Contains(card.CallableTools, want) {
			t.Fatalf("expected callable tool %q to remain: %#v", want, card.CallableTools)
		}
	}
}

func TestRecoveryCardDerivesWriteReadySafeNextFromSourcedEvidence(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools: []string{
				"context_pack", "read_file", "code_navigate", "search_content",
				"mkdir", "edit_file", "write_file", "append_file", "apply_patch", "delegate_edit", "go_move_declarations",
			},
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 internal/app/sessions_panel.go lines 1-219 (total lines: 219)",
		Output:  "func renderSessionsPanelContent() {}\nfunc handleSessionsPanelKey() {}\n",
	}}

	got := runner.renderRecoveryCardForRejectedAction(
		workspace.ToolRequest{Tool: "code_navigate", Path: "internal/app/model.go"},
		workspace.ToolResult{
			Tool:    "code_navigate",
			Summary: "Implementation target write-ready tool rejected",
			Error:   "code_navigate is outside the implementation target-write-ready surface",
		},
		results,
		[]string{"mkdir", "edit_file", "write_file", "append_file", "apply_patch", "delegate_edit", "go_move_declarations"},
	)

	assertRecoveryCardContains(t, got,
		"phase_id=implementation.write_ready",
		"currently callable action classes: per_edit_write, blocker",
		"deterministic safe next action: Emit one parser-readable write to the already-sourced path(s): internal/app/sessions_panel.go. For textual write transport, use one visible JSON object or apply_patch envelope, not key/value edit-body notes; or answer <<QUESTION>> with the exact edit blocker/no-op evidence.",
	)
	if strings.Contains(got, "deterministic safe next action: none available") {
		t.Fatalf("write-ready recovery card should not advertise no safe action, got:\n%s", got)
	}
	assertRecoveryCardOmitsProjectWorkflowTerms(t, got)
}

func TestBuildRecoveryCardDerivesSafeNextForClosedPlannerPhase(t *testing.T) {
	contract := TurnPhaseContract{
		PhaseID:      turnPhaseMalformedSubmitPlanRecovery,
		RequiredNext: []string{"parser-readable submit_plan with real edits or source_satisfied_paths entries for every required_edit_path, Decision: source-satisfied with per-path current-state evidence, or <<QUESTION>> with the exact blocker"},
		ActionPolicy: TurnActionClassPolicy{
			AllowedActionClasses:   []string{actionSubmitPlan, actionSourceSatisfied, actionBlocker},
			InvalidActionClasses:   []string{actionRead, actionSearch, actionPerEditWrite},
			TerminalResponseShapes: []string{"submit_plan", "Decision: source-satisfied", "<<QUESTION>>:"},
			CallableToolNames:      []string{"submit_plan"},
		},
		RuntimeCall: RuntimeCallContract{
			ExpressibleActionClasses: []string{actionSubmitPlan, actionSourceSatisfied, actionBlocker},
		},
	}
	got := RenderRecoveryCard(BuildRecoveryCard(RecoveryCardInput{
		PhaseContract:       contract,
		CurrentAllowedTools: []string{"submit_plan"},
		RejectedRequest:     workspace.ToolRequest{Tool: "read_file", Path: "internal/app/sessions_panel.go"},
		RejectedResult: workspace.ToolResult{
			Tool:    "read_file",
			Summary: "read/search/source-refresh after plan-execute submit_plan readiness",
		},
	}))

	assertRecoveryCardContains(t, got,
		"phase_id=plan_execute.malformed_submit_plan_recovery",
		"currently callable tools: submit_plan",
		"deterministic safe next action: Emit one parser-readable planner outcome: parser-readable submit_plan with real edits or source_satisfied_paths entries for every required_edit_path, Decision: source-satisfied with per-path current-state evidence, or <<QUESTION>> with the exact blocker",
	)
	if strings.Contains(got, "deterministic safe next action: none available") {
		t.Fatalf("closed planner recovery card should not advertise no safe action, got:\n%s", got)
	}
	assertRecoveryCardOmitsProjectWorkflowTerms(t, got)
}

func TestRecoveryCardDoesNotAdvertiseUnavailablePendingRedirect(t *testing.T) {
	contract := TurnPhaseContract{
		PhaseID:      turnPhaseImplementationWriteReady,
		RequiredNext: []string{"parser-readable write to the sourced target path, or <<QUESTION>> with the exact edit blocker/no-op evidence"},
		ActionPolicy: TurnActionClassPolicy{
			AllowedActionClasses: []string{actionPerEditWrite, actionBlocker},
			CallableToolNames:    []string{"apply_patch"},
		},
		RuntimeCall: RuntimeCallContract{
			ExpressibleActionClasses: []string{actionPerEditWrite, actionBlocker},
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Redirected internal/app/sessions_panel.go to code_navigate read_symbol",
	}}
	got := RenderRecoveryCard(BuildRecoveryCard(RecoveryCardInput{
		PhaseContract:               contract,
		CurrentAllowedTools:         []string{"apply_patch"},
		RejectedRequest:             workspace.ToolRequest{Tool: "read_file", Path: "internal/app/sessions_panel.go"},
		RejectedResult:              workspace.ToolResult{Tool: "read_file", Summary: "Implementation target write-ready tool rejected"},
		DeterministicSafeNextAction: recoveryCardSafeNextActionForRejectedAction(workerruntime.WorkerRuntimeBrief{}, contract, results),
	}))

	assertRecoveryCardContains(t, got,
		"phase_id=implementation.write_ready",
		"currently callable tools: apply_patch",
		"currently callable action classes: per_edit_write, blocker",
		"deterministic safe next action: Follow the active write-ready requirement: parser-readable write to the sourced target path, or <<QUESTION>> with the exact edit blocker/no-op evidence",
	)
	if strings.Contains(got, "Resolve the exact pending redirected source-body obligation") ||
		strings.Contains(got, "via code_navigate") {
		t.Fatalf("recovery card must not advertise unavailable pending read follow-up, got:\n%s", got)
	}
}

func TestRecoveryCardAdvertisesCallablePendingRedirect(t *testing.T) {
	contract := TurnPhaseContract{
		PhaseID:      turnPhaseImplementationWriteReady,
		RequiredNext: []string{"resolve the pending redirected source-body obligation, then make one parser-readable write to a sourced target path, or <<QUESTION>> with the exact edit blocker/no-op evidence"},
		ActionPolicy: TurnActionClassPolicy{
			AllowedActionClasses: []string{actionRead, actionPerEditWrite, actionBlocker},
			CallableToolNames:    []string{"code_navigate", "apply_patch"},
		},
		RuntimeCall: RuntimeCallContract{
			ExpressibleActionClasses: []string{actionRead, actionPerEditWrite, actionBlocker},
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Redirected internal/app/sessions_panel.go to code_navigate read_symbol",
	}}
	got := RenderRecoveryCard(BuildRecoveryCard(RecoveryCardInput{
		PhaseContract:               contract,
		CurrentAllowedTools:         []string{"code_navigate", "apply_patch"},
		RejectedRequest:             workspace.ToolRequest{Tool: "search_content", Path: "internal/app"},
		RejectedResult:              workspace.ToolResult{Tool: "search_content", Summary: "Implementation target write-ready pending source-body obligation rejected"},
		DeterministicSafeNextAction: recoveryCardSafeNextActionForRejectedAction(workerruntime.WorkerRuntimeBrief{}, contract, results),
	}))

	assertRecoveryCardContains(t, got,
		"currently callable action classes: read, per_edit_write, blocker",
		"deterministic safe next action: Resolve the exact pending redirected source-body obligation (internal/app/sessions_panel.go via code_navigate capability=read_symbol with pattern/symbol, or one file_outline first when the symbol is unknown), then emit one parser-readable write to a sourced target path. For textual write transport, use one visible JSON object or apply_patch envelope, not key/value edit-body notes; or answer <<QUESTION>> with the exact edit blocker/no-op evidence.",
	)
}

func TestRecoveryCardScopesPendingRedirectsToActiveBrief(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: "implementation",
		Contract:   "implementation_result",
		Objective:  "fix the session pane cursor scrolling",
		TargetPacket: []string{
			"candidate file: internal/app/implementation_dispatch.go",
			"candidate file: internal/app/sessions.go",
			"candidate file: internal/app/sessions_panel.go",
		},
	}
	contract := TurnPhaseContract{
		PhaseID:      turnPhaseImplementationWriteReady,
		RequiredNext: []string{"resolve the pending redirected source-body obligation, then make one parser-readable write to a sourced target path, or <<QUESTION>> with the exact edit blocker/no-op evidence"},
		ActionPolicy: TurnActionClassPolicy{
			AllowedActionClasses: []string{actionRead, actionPerEditWrite, actionBlocker},
			CallableToolNames:    []string{"code_navigate", "apply_patch"},
		},
		RuntimeCall: RuntimeCallContract{
			ExpressibleActionClasses: []string{actionRead, actionPerEditWrite, actionBlocker},
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
		{
			Tool:    "read_file",
			Summary: "Redirected internal/app/sessions.go to code_navigate",
			Output:  "Use code_navigate for source-body evidence in internal/app/sessions.go.",
		},
	}
	got := RenderRecoveryCard(BuildRecoveryCard(RecoveryCardInput{
		PhaseContract:               contract,
		CurrentAllowedTools:         []string{"code_navigate", "apply_patch"},
		RejectedRequest:             workspace.ToolRequest{Tool: "code_navigate", Path: "internal/app/model_state.go"},
		RejectedResult:              workspace.ToolResult{Tool: "code_navigate", Summary: "Implementation target write-ready pending source-body obligation rejected"},
		DeterministicSafeNextAction: recoveryCardSafeNextActionForRejectedAction(brief, contract, results),
	}))

	assertRecoveryCardContains(t, got,
		"deterministic safe next action: Resolve the exact pending redirected source-body obligation (internal/app/sessions.go via code_navigate capability=read_symbol with pattern/symbol, or one file_outline first when the symbol is unknown), then emit one parser-readable write to a sourced target path. For textual write transport, use one visible JSON object or apply_patch envelope, not key/value edit-body notes; or answer <<QUESTION>> with the exact edit blocker/no-op evidence.",
	)
	if strings.Contains(got, "implementation_dispatch.go via code_navigate") {
		t.Fatalf("recovery card must not reintroduce stale scoped-out pending redirect, got:\n%s", got)
	}
}

func TestBuildRecoveryCardDerivesSafeNextForVerificationReadyPhase(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"source_sanity", "shell", "git_diff", "git_status", "apply_patch"},
		},
		Results: []workspace.ToolResult{{
			Tool:    "apply_patch",
			Summary: "Patched files",
			Output:  "update internal/app/sessions_panel.go\n",
		}},
		BaseAllowedTools: []string{"source_sanity", "shell", "git_diff", "git_status", "apply_patch"},
	})
	got := RenderRecoveryCard(BuildRecoveryCard(RecoveryCardInput{
		PhaseContract:       contract,
		CurrentAllowedTools: []string{"source_sanity", "shell", "git_diff", "git_status"},
		RejectedRequest:     workspace.ToolRequest{Tool: "completion_gate"},
		RejectedResult: workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Completion gate rejected final answer",
			Error:   "post-write verification is unresolved",
		},
	}))

	assertRecoveryCardContains(t, got,
		"phase_id=implementation.verify_ready",
		"currently callable action classes: verify, blocker",
		"deterministic safe next action: Run one focused verification action from the active verification requirement: one focused language-appropriate verification command/tool for changed source files, compiler-backed diagnostics, or <<QUESTION>> with the exact verification blocker",
	)
	if strings.Contains(got, "deterministic safe next action: none available") {
		t.Fatalf("verification-ready recovery card should not advertise no safe action, got:\n%s", got)
	}
	assertRecoveryCardOmitsProjectWorkflowTerms(t, got)
}

func TestBuildRecoveryCardDoesNotDeriveVerifyOnlySafeNextForVerificationRepair(t *testing.T) {
	contract := TurnPhaseContract{
		PhaseID:      turnPhaseImplementationVerifyReady,
		Phase:        "verification_repair",
		RequiredNext: []string{"repair the failing in-scope changed/required path, use one narrow source/language/context lookup for an exact repair fact, rerun language-appropriate verification after the repair write, or <<QUESTION>> with the exact external blocker"},
		ActionPolicy: TurnActionClassPolicy{
			AllowedActionClasses: []string{actionPerEditWrite, actionRead, actionVerify, actionBlocker},
			CallableToolNames:    []string{"apply_patch", "read_file", "source_sanity"},
		},
		RuntimeCall: RuntimeCallContract{
			ExpressibleActionClasses: []string{actionPerEditWrite, actionRead, actionVerify, actionBlocker},
		},
	}
	got := RenderRecoveryCard(BuildRecoveryCard(RecoveryCardInput{
		PhaseContract:       contract,
		CurrentAllowedTools: []string{"apply_patch", "read_file", "source_sanity"},
		RejectedRequest:     workspace.ToolRequest{Tool: "completion_gate"},
		RejectedResult: workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Completion gate rejected final answer",
			Error:   "post-repair verification is unresolved",
		},
	}))

	assertRecoveryCardContains(t, got,
		"phase_id=implementation.verify_ready",
		"currently callable action classes: per_edit_write, read, verify, blocker",
		"deterministic safe next action: none available",
	)
	if strings.Contains(got, "Run one focused verification action") {
		t.Fatalf("verification-repair recovery card must not imply verification before repair, got:\n%s", got)
	}
	assertRecoveryCardOmitsProjectWorkflowTerms(t, got)
}

func TestBuildRecoveryCardDoesNotAdvertiseToolsForFinalOnlyPhase(t *testing.T) {
	contract := TurnPhaseContract{
		PhaseID:      turnPhaseGeneric,
		AllowedTools: nil,
		ActionPolicy: TurnActionClassPolicy{
			AllowedActionClasses: []string{actionFinalAnswer, actionBlocker},
		},
		RuntimeCall: RuntimeCallContract{
			ExpressibleActionClasses: []string{actionFinalAnswer, actionBlocker},
		},
	}
	card := BuildRecoveryCard(RecoveryCardInput{
		PhaseContract:       contract,
		CurrentAllowedTools: []string{"read_file", "source_sanity", "shell"},
		RejectedRequest:     workspace.ToolRequest{Tool: "read_file", Path: "internal/labels/labels.go"},
		RejectedResult: workspace.ToolResult{
			Tool:    "read_file",
			Summary: "Conversation contract rejected extra exploration after bounded evidence",
		},
	})
	if len(card.CallableTools) != 0 {
		t.Fatalf("final-only recovery card should not advertise stale tools: %#v", card.CallableTools)
	}
	for _, forbidden := range []string{actionRead, actionVerify, actionGenericProbe} {
		if slices.Contains(card.CallableActionClasses, forbidden) {
			t.Fatalf("final-only recovery card advertised %q: %#v", forbidden, card.CallableActionClasses)
		}
	}
}

func TestRecoveryCardDoesNotSayNoneAvailableWhenFinalAnswerAllowed(t *testing.T) {
	contract := TurnPhaseContract{
		PhaseID: turnPhaseGeneric,
		Phase:   "read_only_evidence_available",
		ActionPolicy: TurnActionClassPolicy{
			AllowedActionClasses: []string{actionFinalAnswer, actionBlocker},
		},
		RuntimeCall: RuntimeCallContract{
			ExpressibleActionClasses: []string{actionFinalAnswer, actionBlocker},
		},
	}
	got := RenderRecoveryCard(BuildRecoveryCard(RecoveryCardInput{
		PhaseContract:   contract,
		RejectedRequest: workspace.ToolRequest{Tool: "code_navigate", Path: "internal/app/model_state.go", Capability: "read_symbol"},
		RejectedResult: workspace.ToolResult{
			Tool:    "code_navigate",
			Summary: "Repeated tool request rejected",
			Error:   "this exact request already replayed prior evidence once",
		},
	}))

	if strings.Contains(got, "deterministic safe next action: none available") {
		t.Fatalf("read-only final-answer recovery must not claim no safe action remains:\n%s", got)
	}
	assertRecoveryCardContains(t, got,
		"currently callable action classes: final_answer, blocker",
		"deterministic safe next action: Let the model choose strategy: answer from gathered evidence now, or state uncertainty with the exact unsupported claim and evidence source.",
	)
}

func TestRecoveryCardEvidencePendingDoesNotSayNoneAvailableWhenFinalAnswerAllowed(t *testing.T) {
	contract := TurnPhaseContract{
		PhaseID: turnPhaseGeneric,
		Phase:   "read_only_evidence_pending",
		RequiredNext: []string{
			"final answer from gathered evidence or explicit uncertainty, one bounded read/search for an exact unsupported claim, or <<QUESTION>> with the exact blocker",
		},
		ActionPolicy: TurnActionClassPolicy{
			AllowedActionClasses: []string{actionBlocker, actionFinalAnswer, actionRead, actionSearch},
			CallableToolNames:    []string{"code_navigate", "read_file", "search_content"},
		},
		RuntimeCall: RuntimeCallContract{
			ExpressibleActionClasses: []string{actionBlocker, actionFinalAnswer, actionRead, actionSearch},
		},
	}
	got := RenderRecoveryCard(BuildRecoveryCard(RecoveryCardInput{
		PhaseContract:       contract,
		CurrentAllowedTools: []string{"code_navigate", "read_file", "search_content"},
		RejectedRequest:     workspace.ToolRequest{Tool: "search_content", Path: ".", Pattern: "key.*input"},
		RejectedResult: workspace.ToolResult{
			Tool:    "search_content",
			Summary: "Discovery consumption required before more broad discovery",
			Error:   "a previous discovery result already returned concrete candidates",
		},
	}))

	if strings.Contains(got, "deterministic safe next action: none available") {
		t.Fatalf("read-only pending recovery must not claim no safe action remains:\n%s", got)
	}
	assertRecoveryCardContains(t, got,
		"currently callable action classes: read, search, blocker, final_answer",
		"deterministic safe next action: Let the model choose strategy: answer from gathered evidence now, or state uncertainty with the exact unsupported claim and evidence source.",
	)
}

func TestBuildRecoveryCardGenericDoesNotInjectProductWorkflowTerms(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			AllowedTools: []string{"read_file", "search_content"},
		},
		BaseAllowedTools: []string{"read_file", "search_content"},
	})
	got := RenderRecoveryCard(BuildRecoveryCard(RecoveryCardInput{
		PhaseContract:       contract,
		CurrentAllowedTools: []string{"read_file", "search_content"},
		RejectedRequest:     workspace.ToolRequest{Tool: "search_content", Pattern: "MissingThing"},
		RejectedResult: workspace.ToolResult{
			Tool:    "search_content",
			Summary: "Zero-hit retry blocked",
			Error:   "this exact search_content call already returned no results earlier this turn",
		},
	}))
	assertRecoveryCardOmitsProjectWorkflowTerms(t, got)
}

func TestCompletionGateRecoveryCardUsesGuardedMutationTargetSourcePhase(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"context_pack", "read_file", "search_content", "edit_file", "apply_patch", "source_sanity"},
		},
	}
	got := runner.renderRecoveryCardForRejectedAction(
		workspace.ToolRequest{Tool: "completion_gate"},
		workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Mutation completion gate rejected final answer",
			Error:   "no successful workspace write",
		},
		nil,
		[]string{"context_pack", "read_file", "search_content", "edit_file", "apply_patch", "source_sanity"},
	)

	assertRecoveryCardContains(t, got,
		"phase_id=implementation.target_source",
		"currently callable action classes:",
		"read",
		"search",
		"per_edit_write",
		"exact blocker shape:",
	)
	if strings.Contains(got, "currently callable action classes: final_answer") ||
		strings.Contains(got, " final_answer") {
		t.Fatalf("guarded no-write recovery card should not advertise final_answer, got:\n%s", got)
	}
	assertRecoveryCardOmitsProjectWorkflowTerms(t, got)
}

func assertRecoveryCardContains(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Fatalf("expected recovery card to contain %q, got:\n%s", want, got)
		}
	}
}

func assertRecoveryCardOmitsProjectWorkflowTerms(t *testing.T, got string) {
	t.Helper()
	for _, forbidden := range []string{"PRD", "ADR", "brief", "product workflow"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("recovery card should stay generic; found %q in:\n%s", forbidden, got)
		}
	}
}
