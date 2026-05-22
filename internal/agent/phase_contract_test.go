package agent

import (
	"slices"
	"strings"
	"testing"

	"idiom/internal/frontier/workerruntime"
	"idiom/internal/workspace"
)

func TestDeriveTurnPhaseContractPlanExecuteRequiredSource(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "code_navigate", "search_content", "submit_plan", "edit_file"},
			RequiredEditPaths: []string{"a.go", "b.go"},
		},
		Results: []workspace.ToolResult{{
			Tool:    "read_file",
			Summary: "Read f1 a.go lines 1-10 (total lines: 10)",
		}},
	})

	if contract.PhaseID != turnPhaseRequiredSource {
		t.Fatalf("PhaseID = %q, want %q", contract.PhaseID, turnPhaseRequiredSource)
	}
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionSourceSatisfied)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionSubmitPlan)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionPerEditWrite)
	assertEqualSet(t, contract.AllowedTools, []string{"read_file", "code_navigate"})
	assertContains(t, contract.HiddenTools, "submit_plan")
}

func TestDeriveTurnPhaseContractSubmitReadyNativeRequired(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "code_navigate", "search_content", "submit_plan", "edit_file"},
			RequiredEditPaths: []string{"a.go", "b.go"},
		},
		Results: []workspace.ToolResult{
			{Tool: "read_file", Summary: "Read f1 a.go lines 1-10 (total lines: 10)"},
			{Tool: "read_file", Summary: "Read f2 b.go lines 1-10 (total lines: 10)"},
		},
		SupportsNativeTools: true,
	})

	if contract.PhaseID != turnPhaseSubmitReadyNativeRequired {
		t.Fatalf("PhaseID = %q, want %q", contract.PhaseID, turnPhaseSubmitReadyNativeRequired)
	}
	assertEqualSet(t, contract.AllowedTools, []string{"submit_plan"})
	assertEqualSet(t, contract.RuntimeCall.ExpressibleActionClasses, []string{actionSubmitPlan})
	assertContains(t, contract.TerminalOutcomes, "Decision: source-satisfied")
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionRead)
	if contract.RuntimeCall.ToolChoice != "required" {
		t.Fatalf("ToolChoice = %q, want required", contract.RuntimeCall.ToolChoice)
	}
}

func TestDeriveTurnPhaseContractSubmitReadyTextual(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "submit_plan"},
			RequiredEditPaths: []string{"a.go"},
		},
		Results: []workspace.ToolResult{{
			Tool:    "read_file",
			Summary: "Read f1 a.go lines 1-10 (total lines: 10)",
		}},
		TextualToolParser: true,
	})

	if contract.PhaseID != turnPhaseSubmitReadyTextual {
		t.Fatalf("PhaseID = %q, want %q", contract.PhaseID, turnPhaseSubmitReadyTextual)
	}
	if len(contract.AllowedTools) != 0 {
		t.Fatalf("textual submit-ready must not advertise manifest-visible tools, got %#v", contract.AllowedTools)
	}
	if len(contract.ActionPolicy.CallableToolNames) != 0 {
		t.Fatalf("textual submit-ready must not advertise callable tool names, got %#v", contract.ActionPolicy.CallableToolNames)
	}
	assertEqualSet(t, contract.RuntimeCall.ExpressibleActionClasses, []string{actionSubmitPlan, actionSourceSatisfied, actionBlocker})
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionSourceSatisfied)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionBlocker)
	if contract.RuntimeCall.ResponseFormat != "json" {
		t.Fatalf("ResponseFormat = %q, want json", contract.RuntimeCall.ResponseFormat)
	}
}

func TestDeriveTurnPhaseContractStaleAnchorRefresh(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "code_navigate", "search_content", "submit_plan"},
			RequiredEditPaths: []string{"a.go"},
		},
		Results: []workspace.ToolResult{
			{Tool: "read_file", Summary: "Read f1 a.go lines 1-10 (total lines: 10)"},
			{
				Tool:    "submit_plan",
				Summary: "submit_plan rejected: required edit path coverage mismatch",
				Error:   "submit_plan must include at least one edit entry for every required_edit_paths entry; invalid edit anchor(s): `a.go: old_text not found in current source`.",
			},
		},
	})

	if contract.PhaseID != turnPhaseStaleAnchorRefresh {
		t.Fatalf("PhaseID = %q, want %q", contract.PhaseID, turnPhaseStaleAnchorRefresh)
	}
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionSubmitPlan)
	assertEqualSet(t, contract.AllowedTools, []string{"read_file", "code_navigate"})
}

func TestDeriveTurnPhaseContractStaleAnchorRefreshWinsOverUnreadRequiredSource(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "code_navigate", "search_content", "submit_plan"},
			RequiredEditPaths: []string{"a.go", "b.go"},
		},
		Results: []workspace.ToolResult{
			{Tool: "read_file", Summary: "Read f1 a.go lines 1-10 (total lines: 10)"},
			{
				Tool:    "submit_plan",
				Summary: "submit_plan rejected: required edit path coverage mismatch",
				Error:   "submit_plan must include at least one edit entry for every required_edit_paths entry; invalid edit anchor(s): `a.go: old_text not found in current source`.",
			},
		},
	})

	if contract.PhaseID != turnPhaseStaleAnchorRefresh {
		t.Fatalf("PhaseID = %q, want %q", contract.PhaseID, turnPhaseStaleAnchorRefresh)
	}
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionSubmitPlan)
}

func TestDeriveTurnPhaseContractMalformedSubmitPlanRecoveryUsesTextualOutput(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "submit_plan"},
			RequiredEditPaths: []string{"a.go"},
		},
		Results: []workspace.ToolResult{
			{Tool: "read_file", Summary: "Read f1 a.go lines 1-10 (total lines: 10)"},
			{Tool: "tool_parse", Summary: "Malformed tool request rejected", Error: "decode tool request"},
		},
		SupportsNativeTools: true,
	})

	if contract.PhaseID != turnPhaseMalformedSubmitPlanRecovery {
		t.Fatalf("PhaseID = %q, want %q", contract.PhaseID, turnPhaseMalformedSubmitPlanRecovery)
	}
	if contract.RuntimeCall.ExpectedVisibleOutput != "visible_text_tool_request" {
		t.Fatalf("ExpectedVisibleOutput = %q, want visible_text_tool_request", contract.RuntimeCall.ExpectedVisibleOutput)
	}
	assertEqualSet(t, contract.RuntimeCall.ExpressibleActionClasses, []string{actionSubmitPlan, actionSourceSatisfied, actionBlocker})
}

func TestDeriveTurnPhaseContractReviewRepairTerminal(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			PermissionProfile: "plan_submission_review",
			AllowedTools:      []string{"read_file", "search_content", "submit_plan"},
		},
		PhaseHint: turnPhaseReviewRepairTerminal,
	})

	if contract.PhaseID != turnPhaseReviewRepairTerminal {
		t.Fatalf("PhaseID = %q, want %q", contract.PhaseID, turnPhaseReviewRepairTerminal)
	}
	assertEqualSet(t, contract.AllowedTools, []string{"submit_plan"})
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionReviewPass)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionSearch)
	assertContains(t, contract.TerminalOutcomes, "Decision: pass")
}

func TestDeriveTurnPhaseContractReviewRepairFailedEvidenceStartsTerminal(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			PermissionProfile: "plan_submission_review",
			AllowedTools:      []string{"read_file", "search_content", "submit_plan"},
			Readiness: &workerruntime.ReadinessState{
				OpenObligations: []string{"source_sanity_failed:internal/writerroute/decision_test.go"},
			},
		},
	})

	if contract.PhaseID != turnPhaseReviewRepairTerminal {
		t.Fatalf("PhaseID = %q, want %q", contract.PhaseID, turnPhaseReviewRepairTerminal)
	}
	assertEqualSet(t, contract.AllowedTools, []string{"submit_plan"})
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionSubmitPlan)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionBlocker)
	assertNotContains(t, contract.ActionPolicy.AllowedActionClasses, actionReviewPass)
	assertNotContains(t, contract.TerminalOutcomes, "Decision: pass")
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionSearch)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionReviewPass)
}

func TestDeriveTurnPhaseContractImplementationWriteReady(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "code_navigate", "search_content", "edit_file", "apply_patch", "shell"},
			RequiredEditPaths: []string{"a.go"},
		},
	})

	if contract.PhaseID != turnPhaseImplementationWriteReady {
		t.Fatalf("PhaseID = %q, want %q", contract.PhaseID, turnPhaseImplementationWriteReady)
	}
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionPerEditWrite)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionGenericProbe)
	assertContains(t, contract.AllowedTools, "edit_file")
	assertContains(t, contract.AllowedTools, "read_file")
	assertContains(t, contract.HiddenTools, "shell")
}

func TestDeriveTurnPhaseContractImplementationVerifyReady(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "code_navigate", "search_content", "edit_file", "source_sanity", "shell", "git_diff", "git_status"},
		},
		Results: []workspace.ToolResult{{
			Tool:    "edit_file",
			Summary: "Edited internal/app/model_swap.go",
		}},
	})

	if contract.PhaseID != turnPhaseImplementationVerifyReady {
		t.Fatalf("PhaseID = %q, want %q", contract.PhaseID, turnPhaseImplementationVerifyReady)
	}
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionVerify)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionBlocker)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionFinalAnswer)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionRead)
	assertContains(t, contract.AllowedTools, "source_sanity")
	assertContains(t, contract.AllowedTools, "shell")
	assertContains(t, contract.HiddenTools, "edit_file")
	assertContains(t, contract.HiddenTools, "read_file")
}

func TestDeriveTurnPhaseContractProductAcceptanceBlocksFinalWithOpenEvidence(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		PhaseHint:          turnPhaseProductAcceptanceCloseout,
		BaseAllowedTools:   []string{"read_file", "search_content"},
		TDDObligationRefs:  []string{"tdd:missing"},
		ResidualBlockers:   []string{"repair packet open"},
		VerificationStatus: "pre_write",
	})

	if contract.PhaseID != turnPhaseProductAcceptanceCloseout {
		t.Fatalf("PhaseID = %q, want %q", contract.PhaseID, turnPhaseProductAcceptanceCloseout)
	}
	assertNotContains(t, contract.ActionPolicy.AllowedActionClasses, actionFinalAnswer)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionFinalAnswer)
	assertEqualSet(t, contract.RuntimeCall.ExpressibleActionClasses, []string{actionBlocker})
	if len(contract.AllowedTools) != 0 {
		t.Fatalf("product closeout must not advertise tools, got %#v", contract.AllowedTools)
	}
	assertEqualSet(t, contract.HiddenTools, []string{"read_file", "search_content"})
	assertContains(t, contract.EvidenceState.TDDObligationRefs, "tdd:missing")
}

func TestDeriveTurnPhaseContractProductAcceptanceBlocksSkippedWorkflow(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		PhaseHint:            turnPhaseProductAcceptanceCloseout,
		ProductWorkflowState: "skipped",
	})

	assertNotContains(t, contract.ActionPolicy.AllowedActionClasses, actionFinalAnswer)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionFinalAnswer)
}

func TestDeriveTurnPhaseContractArtifactFinalAnswerNoTools(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        workerruntime.ArtifactFinalAnswerTaskFamily,
			Contract:          "artifact_final_answer",
			PermissionProfile: "conversation_only",
		},
		BaseAllowedTools:    []string{"read_file", "search_content", "write_file", "project_lookup"},
		ToolTransportMode:   "textual",
		TextualToolParser:   true,
		SupportsNativeTools: false,
	})

	if contract.PhaseID != turnPhaseArtifactFinalAnswer {
		t.Fatalf("PhaseID = %q, want %q", contract.PhaseID, turnPhaseArtifactFinalAnswer)
	}
	if len(contract.AllowedTools) != 0 || len(contract.ActionPolicy.CallableToolNames) != 0 {
		t.Fatalf("artifact final-answer must not advertise callable tools, got allowed=%#v callable=%#v", contract.AllowedTools, contract.ActionPolicy.CallableToolNames)
	}
	assertContains(t, contract.HiddenTools, "project_lookup")
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionFinalAnswer)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionSubmitPlan)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionPerEditWrite)
	if contract.RuntimeCall.ExpectedVisibleOutput != "final_answer_artifact" {
		t.Fatalf("ExpectedVisibleOutput = %q, want final_answer_artifact", contract.RuntimeCall.ExpectedVisibleOutput)
	}
	assertEqualSet(t, contract.RuntimeCall.ExpressibleActionClasses, []string{actionFinalAnswer, actionBlocker})
}

func TestApplyTurnPhaseContractToNextActionStateReplacesForbiddenProbeGuidance(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "search_content", "submit_plan"},
			RequiredEditPaths: []string{"a.go"},
		},
		Results: []workspace.ToolResult{{Tool: "read_file", Summary: "Read f1 a.go lines 1-10 (total lines: 10)"}},
	})
	state := runnerNextActionState{
		Phase:              "generic",
		NextValidActions:   []string{"read another file", "answer from evidence"},
		InvalidNextActions: []string{"unrelated write"},
	}

	state = applyTurnPhaseContractToNextActionState(state, contract)

	if state.Phase != "submit_ready_textual" {
		t.Fatalf("Phase = %q, want submit_ready_textual", state.Phase)
	}
	joinedNext := strings.Join(state.NextValidActions, "\n")
	if !strings.Contains(joinedNext, "submit_plan") || strings.Contains(joinedNext, "read another file") {
		t.Fatalf("next actions should come from phase contract, got %#v", state.NextValidActions)
	}
	joinedInvalid := strings.Join(state.InvalidNextActions, "\n")
	if !strings.Contains(joinedInvalid, "read/search") {
		t.Fatalf("invalid actions should come from phase contract, got %#v", state.InvalidNextActions)
	}
}

func assertContains(t *testing.T, values []string, want string) {
	t.Helper()
	if !slices.Contains(values, want) {
		t.Fatalf("expected %q in %#v", want, values)
	}
}

func assertNotContains(t *testing.T, values []string, want string) {
	t.Helper()
	if slices.Contains(values, want) {
		t.Fatalf("did not expect %q in %#v", want, values)
	}
}

func assertEqualSet(t *testing.T, got []string, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %#v, want set %#v", got, want)
	}
	for _, value := range want {
		if !slices.Contains(got, value) {
			t.Fatalf("got %#v, want set containing %q", got, value)
		}
	}
}
