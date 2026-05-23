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

func TestDeriveTurnPhaseContractPlanExecuteCandidateSourcePending(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			PermissionProfile: "guarded_workspace_write",
			AllowedTools: []string{
				"context_pack", "read_file", "code_navigate", "product_efforts", "product_status",
				"submit_plan", "edit_file", "shell",
			},
		},
	})

	if contract.PhaseID != turnPhaseCandidateSource {
		t.Fatalf("PhaseID = %q, want %q", contract.PhaseID, turnPhaseCandidateSource)
	}
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionSourceSatisfied)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionSubmitPlan)
	assertEqualSet(t, contract.AllowedTools, []string{"context_pack", "read_file", "code_navigate", "product_efforts", "product_status"})
	assertContains(t, contract.HiddenTools, "submit_plan")
}

func TestDeriveTurnPhaseContractPlanExecuteCandidateSourceReadSubmitReady(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"context_pack", "read_file", "submit_plan"},
		},
		Results: []workspace.ToolResult{{
			Tool:    "read_file",
			Summary: "Read internal/workspace/product_tools.go lines 1-114 (total lines: 114)",
			Output:  "func (m *Manager) productEfforts(req ToolRequest) ToolResult { return ToolResult{} }",
		}},
	})

	if contract.PhaseID != turnPhaseSubmitReadyTextual {
		t.Fatalf("PhaseID = %q, want %q", contract.PhaseID, turnPhaseSubmitReadyTextual)
	}
	assertContains(t, contract.AllowedTools, "submit_plan")
	assertContains(t, contract.HiddenTools, "read_file")
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

func TestDeriveTurnPhaseContractImplementationTargetSourcePending(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools: []string{
				"context_pack", "search_content", "read_file", "code_navigate",
				"edit_file", "write_file", "apply_patch", "source_sanity", "shell",
			},
		},
	})

	if contract.PhaseID != turnPhaseImplementationTargetSource {
		t.Fatalf("PhaseID = %q, want %q", contract.PhaseID, turnPhaseImplementationTargetSource)
	}
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionSearch)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionPerEditWrite)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionBlocker)
	assertNotContains(t, contract.ActionPolicy.AllowedActionClasses, actionFinalAnswer)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionFinalAnswer)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionVerify)
	assertContains(t, contract.AllowedTools, "context_pack")
	assertContains(t, contract.AllowedTools, "read_file")
	assertContains(t, contract.AllowedTools, "edit_file")
	assertContains(t, contract.HiddenTools, "source_sanity")
	joinedNext := strings.Join(contract.RequiredNext, "\n")
	for _, want := range []string{"source-body evidence", "parser-readable write", "<<QUESTION>>"} {
		if !strings.Contains(joinedNext, want) {
			t.Fatalf("RequiredNext missing %q: %#v", want, contract.RequiredNext)
		}
	}
}

func TestDeriveTurnPhaseContractImplementationTargetSourceDoesNotOverrideWriteReady(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "edit_file", "source_sanity"},
			Readiness: &workerruntime.ReadinessState{
				ReadyToWrite: true,
			},
		},
	})

	if contract.PhaseID == turnPhaseImplementationTargetSource {
		t.Fatalf("write-ready packet should not use target-source phase: %#v", contract)
	}
}

func TestDeriveTurnPhaseContractPlanExecuteAllowedScopeDoesNotUseImplementationTargetSource(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"context_pack", "read_file", "code_navigate", "search_content", "submit_plan", "edit_file", "apply_patch"},
			AllowedEditPaths:  []string{"pkg/feature.go", "pkg/feature_test.go"},
		},
	})

	if contract.PhaseID != turnPhaseCandidateSource {
		t.Fatalf("PhaseID = %q, want %q", contract.PhaseID, turnPhaseCandidateSource)
	}
	assertNotContains(t, contract.ActionPolicy.AllowedActionClasses, actionPerEditWrite)
	assertNotContains(t, contract.AllowedTools, "edit_file")
	assertNotContains(t, contract.AllowedTools, "apply_patch")
}

func TestDeriveTurnPhaseContractImplementationVerifyReady(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			RequiredEditPaths: []string{"internal/app/model_swap.go"},
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

func TestDeriveTurnPhaseContractUnscopedImplementationVerifyReadyRequiresVerificationFirst(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "code_navigate", "search_content", "edit_file", "source_sanity", "shell", "git_diff", "git_status"},
		},
		Results: []workspace.ToolResult{{
			Tool:    "edit_file",
			Summary: "Edited pkg/feature.go",
		}},
	})

	if contract.PhaseID != turnPhaseImplementationVerifyReady {
		t.Fatalf("PhaseID = %q, want %q", contract.PhaseID, turnPhaseImplementationVerifyReady)
	}
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionVerify)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionPerEditWrite)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionRead)
	assertContains(t, contract.AllowedTools, "source_sanity")
	assertNotContains(t, contract.AllowedTools, "edit_file")
	assertNotContains(t, contract.AllowedTools, "read_file")
	assertContains(t, contract.HiddenTools, "edit_file")
	assertContains(t, contract.HiddenTools, "read_file")
}

func TestDeriveTurnPhaseContractUnscopedImplementationWriteReadyAfterSourceBody(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "search_content", "edit_file", "apply_patch", "source_sanity"},
		},
		Results: []workspace.ToolResult{{
			Tool:    "read_file",
			Summary: "Read f1 internal/labels/labels.go lines 1-5 (total lines: 5)",
			Output:  "func ShortLabel() string {\n\treturn \"Save changes\"\n}\n",
		}},
	})

	if contract.Phase != "target_write_ready" {
		t.Fatalf("Phase = %q, want target_write_ready", contract.Phase)
	}
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionPerEditWrite)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionRead)
	assertContains(t, contract.AllowedTools, "edit_file")
	assertContains(t, contract.AllowedTools, "apply_patch")
	assertNotContains(t, contract.AllowedTools, "read_file")
	assertNotContains(t, contract.AllowedTools, "search_content")
}

func TestDeriveTurnPhaseContractUnscopedImplementationStaysTargetSourceAfterSymbolMiss(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "code_navigate", "search_content", "edit_file", "apply_patch"},
		},
		Results: []workspace.ToolResult{{
			Tool:    "code_navigate",
			Summary: "Code navigation routed read_symbol via go_read_symbol",
			Output:  "capability: read_symbol\nlanguage: go\nrouted_tool: go_read_symbol\nroute_summary: No Go symbols matched \"save\"",
		}},
	})

	if contract.PhaseID != turnPhaseImplementationTargetSource {
		t.Fatalf("PhaseID = %q, want target_source after symbol miss; contract=%#v", contract.PhaseID, contract)
	}
	assertContains(t, contract.AllowedTools, "read_file")
	assertContains(t, contract.AllowedTools, "code_navigate")
}

func TestDeriveTurnPhaseContractReadOnlyWorkspaceRequiresEvidenceBeforeAnswer(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "direct_answer",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"search_files", "search_content", "read_file", "edit_file", "shell"},
			Readiness:         &workerruntime.ReadinessState{ReadyToAnswer: false, OpenObligations: []string{"evidence"}},
		},
	})

	if contract.Phase != "read_only_evidence_pending" {
		t.Fatalf("Phase = %q, want read_only_evidence_pending", contract.Phase)
	}
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionSearch)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionFinalAnswer)
	assertContains(t, contract.AllowedTools, "read_file")
	assertContains(t, contract.AllowedTools, "search_content")
	assertNotContains(t, contract.AllowedTools, "edit_file")
	assertNotContains(t, contract.AllowedTools, "shell")
}

func TestDeriveTurnPhaseContractReadOnlyWorkspaceAllowsAnswerAfterEvidence(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "direct_answer",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"search_content", "read_file"},
			Readiness:         &workerruntime.ReadinessState{ReadyToAnswer: false, OpenObligations: []string{"evidence"}},
		},
		Results: []workspace.ToolResult{{
			Tool:    "read_file",
			Summary: "Read f1 internal/labels/labels.go lines 1-5 (total lines: 5)",
		}},
	})

	if contract.Phase != "generic" {
		t.Fatalf("Phase = %q, want generic after evidence", contract.Phase)
	}
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionFinalAnswer)
	assertNotContains(t, contract.ActionPolicy.InvalidActionClasses, actionFinalAnswer)
}

func TestDeriveTurnPhaseContractReadOnlyConversationEvidenceReadyDoesNotAdvertiseTools(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"search_content", "read_file", "code_navigate"},
		},
		Results: []workspace.ToolResult{
			{Tool: "read_file", Summary: "Read f1 internal/labels/labels.go lines 1-5"},
			{Tool: "code_navigate", Summary: "Code navigation routed references via go_references"},
		},
	})

	if contract.Phase != "read_only_evidence_ready" {
		t.Fatalf("Phase = %q, want read_only_evidence_ready", contract.Phase)
	}
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionFinalAnswer)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionBlocker)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionRead)
	if len(contract.AllowedTools) != 0 {
		t.Fatalf("read-only answer-ready phase must not advertise tools, got %#v", contract.AllowedTools)
	}
	if len(contract.ActionPolicy.CallableToolNames) != 0 {
		t.Fatalf("read-only answer-ready policy must not advertise callable tools, got %#v", contract.ActionPolicy.CallableToolNames)
	}
}

func TestDeriveTurnPhaseContractReadOnlyWorkspaceStaysPendingAfterShallowDiscovery(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"search_files", "context_pack", "read_file"},
			Readiness:         &workerruntime.ReadinessState{ReadyToAnswer: false, OpenObligations: []string{"evidence"}},
		},
		Results: []workspace.ToolResult{
			{Tool: "search_files", Summary: "Matched 1 file", Output: "internal/labels/labels.go"},
		},
	})

	if contract.Phase != "read_only_evidence_pending" {
		t.Fatalf("Phase = %q, want read_only_evidence_pending after shallow discovery; contract=%#v", contract.Phase, contract)
	}
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionFinalAnswer)
	assertContains(t, contract.AllowedTools, "read_file")
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
