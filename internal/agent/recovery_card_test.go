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
		"exact blocker shape: <<QUESTION>>: Cannot verify <claim> because the search returned zero candidate paths and no source anchor is available.",
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
		"exact blocker shape: <<QUESTION>>: Cannot resolve <claim> because <specific source range or capability> is unavailable after the repeated read.",
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
		"exact blocker shape: <<QUESTION>>: Verification blocked because <exact command or capability> cannot run: <specific reason>.",
	)
	assertRecoveryCardOmitsProjectWorkflowTerms(t, got)
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
