package agent

import (
	"slices"
	"strings"
	"testing"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
	"idiom/internal/contracts/compile"
	"idiom/internal/runtime/workerruntime"
)

func TestPhaseActionVocabularyUsesAutonomyConstants(t *testing.T) {
	want := map[string]autonomy.ActionClass{
		actionRead:             autonomy.ActionClassRead,
		actionSearch:           autonomy.ActionClassSearch,
		actionSubmitPlan:       autonomy.ActionClassSubmitPlan,
		actionPerEditWrite:     autonomy.ActionClassPerEditWrite,
		actionDeliverableWrite: autonomy.ActionClassDeliverableWrite,
		actionSourceSatisfied:  autonomy.ActionClassSourceSatisfied,
		actionBlocker:          autonomy.ActionClassBlocker,
		actionVerify:           autonomy.ActionClassVerify,
		actionFinalAnswer:      autonomy.ActionClassFinalAnswer,
		actionReviewPass:       autonomy.ActionClassReviewPass,
		actionGenericProbe:     autonomy.ActionClassGenericProbe,
		actionProseFinal:       autonomy.ActionClassProseFinal,
	}
	for got, class := range want {
		if got != autonomy.ActionClassToken(class) {
			t.Fatalf("phase action alias %q does not match autonomy class %q", got, class)
		}
	}
}

func TestGenericAllowedActionClassesUsesAutonomyToolMapping(t *testing.T) {
	classes := genericAllowedActionClasses([]string{"history_range", "history_symbol", "context_status", "apply_patch"})

	assertContains(t, classes, actionRead)
	assertContains(t, classes, actionSearch)
	assertContains(t, classes, actionPerEditWrite)
	assertContains(t, classes, actionFinalAnswer)
	assertContains(t, classes, actionBlocker)
	assertNotContains(t, classes, "history")
}

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
	assertNotContains(t, contract.ActionPolicy.CallableToolNames, "submit_plan")
}

func TestPlanExecutePlanningStateRequiredSourceProjectionSplitsParserVisibleSubmit(t *testing.T) {
	state := DerivePlanExecutePlanningState(workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.go", "b.go"},
	}, []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 a.go lines 1-10 (total lines: 10)",
	}}, PlanExecutePlanningTransport{TextualToolParser: true})

	if state.Mode != PlanExecutePlanningModeRequiredSource {
		t.Fatalf("Mode = %q, want %q", state.Mode, PlanExecutePlanningModeRequiredSource)
	}

	assertNotContains(t, state.ParserVisibleTools, "submit_plan")
	assertNotContains(t, state.CallableTools, "submit_plan")
	assertNotContains(t, state.AllowedActionClasses, actionSubmitPlan)
	assertContains(t, state.InvalidActionClasses, actionSubmitPlan)
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
	assertNotContains(t, contract.ActionPolicy.CallableToolNames, "submit_plan")
}

func TestPlanExecutePlanningStateCandidateProjectionRequiresSelectedPathForSubmit(t *testing.T) {
	state := DerivePlanExecutePlanningState(workerruntime.WorkerRuntimeBrief{
		TaskFamily: PlanThenExecutePlannerTaskFamily,
	}, nil, PlanExecutePlanningTransport{TextualToolParser: true})

	if state.Mode != PlanExecutePlanningModeAllowedCandidate {
		t.Fatalf("Mode = %q, want %q", state.Mode, PlanExecutePlanningModeAllowedCandidate)
	}

	assertNotContains(t, state.ParserVisibleTools, "submit_plan")
	assertNotContains(t, state.CallableTools, "submit_plan")
	assertNotContains(t, state.AllowedActionClasses, actionSubmitPlan)
	assertContains(t, state.InvalidActionClasses, actionSubmitPlan)
}

func TestPlanExecutePlanningStateCandidateProjectionAllowsSubmitWithSelectedPath(t *testing.T) {
	state := DerivePlanExecutePlanningState(workerruntime.WorkerRuntimeBrief{
		TaskFamily:       PlanThenExecutePlannerTaskFamily,
		AllowedEditPaths: []string{"a.go", "b.go"},
	}, []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 a.go lines 1-10 (total lines: 10)",
	}}, PlanExecutePlanningTransport{TextualToolParser: true})

	if state.Mode != PlanExecutePlanningModeAllowedCandidate {
		t.Fatalf("Mode = %q, want %q", state.Mode, PlanExecutePlanningModeAllowedCandidate)
	}

	assertContains(t, state.SelectedEditPaths, "a.go")
	assertContains(t, state.ParserVisibleTools, "submit_plan")
	assertContains(t, state.CallableTools, "submit_plan")
	assertContains(t, state.AllowedActionClasses, actionSubmitPlan)
	assertNotContains(t, state.InvalidActionClasses, actionSubmitPlan)
	assertContains(t, state.TerminalResponseShapes, "submit_plan")
}

func TestDeriveTurnPhaseContractPlanExecuteOpenCandidateSourceRequiresConcreteSelectedPathBeforeSubmit(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"context_pack", "read_file", "search_content", "submit_plan"},
		},
		Results: []workspace.ToolResult{{
			Tool:    "read_file",
			Summary: "Read internal/capabilities/workspace/product_tools.go lines 1-114 (total lines: 114)",
			Output:  "func (m *Manager) productEfforts(req ToolRequest) ToolResult { return ToolResult{} }",
		}},
	})

	if contract.PhaseID != turnPhaseCandidateSource {
		t.Fatalf("PhaseID = %q, want %q", contract.PhaseID, turnPhaseCandidateSource)
	}

	assertContains(t, contract.AllowedTools, "read_file")
	assertContains(t, contract.AllowedTools, "search_content")
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionSubmitPlan)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
}

func TestDeriveTurnPhaseContractPlanExecuteTerminalRecoveryHidesReadActions(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"submit_plan"},
		},
		Results: []workspace.ToolResult{{
			Tool:    "read_file",
			Summary: "Read internal/workflows/productworkflow/coverage_gate.go lines 1-125 (total lines: 391)",
			Output:  "func EvaluateCoverageGate() {}",
		}},
	})

	if contract.PhaseID != turnPhaseCandidateSource {
		t.Fatalf("PhaseID = %q, want %q", contract.PhaseID, turnPhaseCandidateSource)
	}

	if len(contract.AllowedTools) != 0 || len(contract.ActionPolicy.CallableToolNames) != 0 {
		t.Fatalf("candidate source without selected edit paths must not advertise submit_plan as callable, got allowed=%#v callable=%#v", contract.AllowedTools, contract.ActionPolicy.CallableToolNames)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionSubmitPlan)
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
		Temperature:         0,
		TopP:                1,
		TopK:                20,
		MinP:                0.05,
		PresencePenalty:     0.1,
		RepetitionPenalty:   1.1,
		SupportsNativeTools: true,
	})

	if contract.PhaseID != turnPhaseSubmitReadyNativeRequired {
		t.Fatalf("PhaseID = %q, want %q", contract.PhaseID, turnPhaseSubmitReadyNativeRequired)
	}

	assertEqualSet(t, contract.AllowedTools, []string{"submit_plan"})
	assertEqualSet(t, contract.RuntimeCall.ExpressibleActionClasses, []string{actionSubmitPlan})
	assertEqualSet(t, contract.TerminalOutcomes, []string{"submit_plan"})
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionRead)

	if contract.RuntimeCall.ToolChoice != "required" {
		t.Fatalf("ToolChoice = %q, want required", contract.RuntimeCall.ToolChoice)
	}

	if contract.RuntimeCall.Temperature != 0 || contract.RuntimeCall.TopP != 1 || contract.RuntimeCall.TopK != 20 || contract.RuntimeCall.MinP != 0.05 || contract.RuntimeCall.PresencePenalty != 0.1 || contract.RuntimeCall.RepetitionPenalty != 1.1 {
		t.Fatalf("runtime sampling not projected into phase contract: %#v", contract.RuntimeCall)
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

func TestPlanExecutePlanningStateSubmitReadyProjectionMatchesTransport(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.go"},
	}
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Summary: "Read f1 a.go lines 1-10 (total lines: 10)",
	}}

	textual := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true, ResponseFormatJSON: true})
	if textual.Mode != PlanExecutePlanningModeSubmitReady {
		t.Fatalf("textual Mode = %q, want %q", textual.Mode, PlanExecutePlanningModeSubmitReady)
	}

	assertEqualSet(t, textual.ParserVisibleTools, []string{"submit_plan"})
	assertEqualSet(t, textual.CallableTools, []string{"submit_plan"})
	assertEqualSet(t, textual.AllowedActionClasses, []string{actionSubmitPlan, actionSourceSatisfied, actionBlocker})
	assertContains(t, textual.InvalidActionClasses, actionRead)
	assertEqualSet(t, textual.TerminalResponseShapes, []string{"submit_plan", "Decision: source-satisfied", "<<QUESTION>>:"})

	native := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{SupportsNativeTools: true})
	if native.Mode != PlanExecutePlanningModeSubmitReady {
		t.Fatalf("native Mode = %q, want %q", native.Mode, PlanExecutePlanningModeSubmitReady)
	}

	assertEqualSet(t, native.ParserVisibleTools, []string{"submit_plan"})
	assertEqualSet(t, native.CallableTools, []string{"submit_plan"})
	assertEqualSet(t, native.AllowedActionClasses, []string{actionSubmitPlan})
	assertContains(t, native.InvalidActionClasses, actionRead)
	assertEqualSet(t, native.TerminalResponseShapes, []string{"submit_plan"})
}

func TestPlanExecutePlannerAuthorityProjectionParity(t *testing.T) {
	tests := []struct {
		name        string
		brief       workerruntime.WorkerRuntimeBrief
		results     []workspace.ToolResult
		wantMode    PlanExecutePlanningMode
		wantPhaseID string
		wantTools   []string
	}{
		{
			name: "required-source",
			brief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        PlanThenExecutePlannerTaskFamily,
				PermissionProfile: "guarded_workspace_write",
				AllowedTools:      []string{"read_file", "code_navigate", "search_content", "submit_plan", "edit_file"},
				RequiredEditPaths: []string{"a.go", "b.go"},
			},
			results: []workspace.ToolResult{{
				Tool:    "read_file",
				Summary: "Read f1 a.go lines 1-10 (total lines: 10)",
				Output:  "1:package a\n",
			}},
			wantMode:    PlanExecutePlanningModeRequiredSource,
			wantPhaseID: turnPhaseRequiredSource,
			wantTools:   []string{"read_file", "code_navigate"},
		},
		{
			name: "history-pending exact source read",
			brief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        PlanThenExecutePlannerTaskFamily,
				PermissionProfile: "guarded_workspace_write",
				AllowedTools:      []string{"read_file", "code_navigate", "history_range", "history_symbol", "submit_plan"},
				RequiredEditPaths: []string{"pkg/feature.go"},
				RequiredEvidence:  []string{requiredEvidenceGitHistoryRationale},
			},
			results: []workspace.ToolResult{{
				Tool:    "read_file",
				Summary: "Read f1 pkg/feature.go lines 1-10 (total lines: 10)",
				Output:  "1:package pkg\n2:func Feature() bool { return true }\n",
			}},
			wantMode:    PlanExecutePlanningModeHistoryPending,
			wantPhaseID: turnPhaseHistoryPending,
			wantTools:   []string{"history_range", "history_symbol"},
		},
		{
			name: "submit-ready",
			brief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        PlanThenExecutePlannerTaskFamily,
				PermissionProfile: "guarded_workspace_write",
				AllowedTools:      []string{"read_file", "code_navigate", "search_content", "submit_plan", "edit_file"},
				RequiredEditPaths: []string{"a.go"},
			},
			results: []workspace.ToolResult{{
				Tool:    "read_file",
				Summary: "Read f1 a.go lines 1-10 (total lines: 10)",
				Output:  "1:package a\n",
			}},
			wantMode:    PlanExecutePlanningModeSubmitReady,
			wantPhaseID: turnPhaseSubmitReadyTextual,
			wantTools:   []string{"submit_plan"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := DerivePlanExecutePlanningState(tt.brief, tt.results, PlanExecutePlanningTransport{TextualToolParser: true})
			if state.Mode != tt.wantMode {
				t.Fatalf("planning state mode = %q, want %q", state.Mode, tt.wantMode)
			}

			assertEqualSet(t, state.CallableTools, tt.wantTools)

			contract := deriveTurnPhaseContract(TurnPhaseContractInput{
				Brief:            tt.brief,
				Results:          tt.results,
				BaseAllowedTools: tt.brief.AllowedTools,
			})
			if contract.PhaseID != tt.wantPhaseID {
				t.Fatalf("phase contract PhaseID = %q, want %q", contract.PhaseID, tt.wantPhaseID)
			}

			assertEqualSet(t, contract.AllowedTools, tt.wantTools)
			assertEqualSet(t, contract.ActionPolicy.CallableToolNames, tt.wantTools)

			textualAllowed := runnerPlanExecuteTextualAllowedTools(nil, tt.brief, tt.results)
			assertEqualSet(t, textualAllowed, tt.wantTools)

			input, ok := runnerPlanExecutePlannerInput(tt.brief, tt.results, contract, RunPolicy{}, false, contract.ContractID)
			if !ok {
				t.Fatal("expected planner compiler input")
			}

			compiled, err := contractcompile.CompilePlanExecutePlanner(input)
			if err != nil {
				t.Fatalf("compile planner contract: %v", err)
			}

			assertEqualSet(t, compiled.Protocol.VisibleTools, contract.AllowedTools)
			assertEqualSet(t, compiled.Protocol.CallableTools, contract.ActionPolicy.CallableToolNames)
			assertEqualSet(t, compiled.Protocol.CallableActionClasses, contract.ActionPolicy.AllowedActionClasses)
			assertEqualSet(t, compiled.Protocol.InvalidActionClasses, contract.ActionPolicy.InvalidActionClasses)
			assertEqualSet(t, compiled.Protocol.TerminalResponseShapes, contract.ActionPolicy.TerminalResponseShapes)
			assertEqualSet(t, compiled.Protocol.SourceBodyObligations, input.Protocol.SourceBodyObligations)

			protocol, ok := (&Runner{}).compileModelProtocolProjection(tt.brief, tt.results, contract, RunPolicy{}, false)
			if !ok {
				t.Fatal("expected model protocol projection")
			}

			if protocol.PhaseID != contract.PhaseID {
				t.Fatalf("model protocol PhaseID = %q, want %q", protocol.PhaseID, contract.PhaseID)
			}

			assertEqualSet(t, protocol.VisibleTools, contract.AllowedTools)
			assertEqualSet(t, protocol.CallableTools, contract.ActionPolicy.CallableToolNames)
			assertEqualSet(t, protocol.CallableActionClasses, contract.ActionPolicy.AllowedActionClasses)
			assertEqualSet(t, protocol.InvalidActionClasses, contract.ActionPolicy.InvalidActionClasses)
			assertEqualSet(t, protocol.TerminalResponseShapes, contract.ActionPolicy.TerminalResponseShapes)
			assertEqualSet(t, protocol.SourceBodyObligations, input.Protocol.SourceBodyObligations)
		})
	}
}

func TestDeriveTurnPhaseContractStaleAnchorRefresh(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "code_navigate", "search_content", "submit_plan"},
			RequiredEditPaths: []string{"a.go"},
			StopConditions:    []string{"submit_plan_returned", "source_satisfied_reported", "path_specific_blocker_reported"},
		},
		Results: []workspace.ToolResult{
			{Tool: "read_file", Summary: "Read f1 a.go lines 1-10 (total lines: 10)"},
			{
				Tool:    "submit_plan",
				Summary: "submit_plan rejected: required edit path coverage mismatch",
				Error:   "submit_plan must use edits[] for changed required paths and top-level source_satisfied_paths for already-final required paths; invalid edit anchor(s): `a.go: old_text not found in current source`.",
			},
		},
	})

	if contract.PhaseID != turnPhaseStaleAnchorRefresh {
		t.Fatalf("PhaseID = %q, want %q", contract.PhaseID, turnPhaseStaleAnchorRefresh)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertNotContains(t, contract.ActionPolicy.AllowedActionClasses, actionSourceSatisfied)
	assertNotContains(t, contract.ActionPolicy.AllowedActionClasses, actionBlocker)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionSubmitPlan)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionSourceSatisfied)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionBlocker)
	assertEqualSet(t, contract.ActionPolicy.TerminalResponseShapes, nil)
	assertEqualSet(t, contract.StopConditions, []string{"bounded_source_refresh_returned"})
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
				Error:   "submit_plan must use edits[] for changed required paths and top-level source_satisfied_paths for already-final required paths; invalid edit anchor(s): `a.go: old_text not found in current source`.",
			},
		},
	})

	if contract.PhaseID != turnPhaseStaleAnchorRefresh {
		t.Fatalf("PhaseID = %q, want %q", contract.PhaseID, turnPhaseStaleAnchorRefresh)
	}

	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionSubmitPlan)
}

func TestPlanExecutePlanningStateAnchorRefreshWinsOverCandidateReadyAndHistory(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"read_file", "code_navigate", "history_range", "history_symbol", "submit_plan"},
		RequiredEditPaths: []string{"a.go"},
		RequiredEvidence:  []string{requiredEvidenceGitHistoryRationale},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.go lines 1-10 (total lines: 10)", Output: "1:package a\n"},
		{Tool: "history_range", Summary: "Read git history for a.go lines 1-10", Output: "commit abc123\n\n    explain current behavior"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "submit_plan must use edits[] for changed required paths and top-level source_satisfied_paths for already-final required paths; invalid edit anchor(s): `a.go: old_text not found in current source`.",
		},
		{
			Tool:    "read_file",
			Code:    planThenExecuteResultCandidateSourceReady,
			Summary: "Candidate source already ready",
			Error:   "terminal planner recovery active",
		},
	}

	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode != PlanExecutePlanningModeAnchorRefresh {
		t.Fatalf("mode = %q, want anchor_refresh", state.Mode)
	}

	assertEqualSet(t, state.ParserVisibleTools, []string{"read_file", "code_navigate"})

	if slices.Contains(state.ParserVisibleTools, "submit_plan") {
		t.Fatalf("anchor-refresh state must not expose submit_plan: %#v", state.ParserVisibleTools)
	}

	withoutHistory := []workspace.ToolResult{results[0], results[2]}

	state = DerivePlanExecutePlanningState(brief, withoutHistory, PlanExecutePlanningTransport{TextualToolParser: true})

	if state.Mode != PlanExecutePlanningModeAnchorRefresh {
		t.Fatalf("mode with pending history = %q, want anchor_refresh", state.Mode)
	}

	assertEqualSet(t, state.ParserVisibleTools, []string{"read_file", "code_navigate"})

	if summary, errorText, reject := planThenExecutePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      "a.go",
		StartLine: 1,
		EndLine:   10,
	}, brief, withoutHistory); reject {
		t.Fatalf("pending history must not block bounded anchor refresh, got %q: %s", summary, errorText)
	}

	withRefresh := append(append([]workspace.ToolResult(nil), withoutHistory...), workspace.ToolResult{
		Tool:    "read_file",
		Summary: "Read a.go lines 1-10 (total lines: 10)",
		Output:  "1:package a\n",
	})

	state = DerivePlanExecutePlanningState(brief, withRefresh, PlanExecutePlanningTransport{TextualToolParser: true})

	if state.Mode != PlanExecutePlanningModeHistoryPending {
		t.Fatalf("mode after anchor refresh with pending history = %q, want history_pending", state.Mode)
	}

	if summary, errorText, reject := planThenExecutePostReadinessDiscoveryRejection(workspace.ToolRequest{
		Tool:      "history_range",
		Path:      "a.go",
		StartLine: 1,
		EndLine:   10,
	}, brief, withRefresh); reject {
		t.Fatalf("post-refresh pending history request should be allowed, got %q: %s", summary, errorText)
	}
}

func TestPlanExecuteRecoverySurfaceAnchorRefreshWinsOverSourceSatisfiedTerminal(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"read_file", "code_navigate", "submit_plan"},
		RequiredEditPaths: []string{"a.go"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.go lines 1-10 (total lines: 10)", Output: "1:package a\n"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Error:   "invalid edit anchor(s): `a.go: old_text not found in current source`.",
		},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: source-satisfied terminal shape required",
			Error:   "submit_plan cannot carry a source-satisfied/no-op closeout. Answer `Decision: source-satisfied` with per-path current-state evidence, submit real edits, or answer <<QUESTION>> with the exact blocker.",
			Code:    workspace.SubmitPlanSourceSatisfiedTerminalRequiredCode,
		},
	}

	surface := DerivePlanExecuteRecoverySurface(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if surface.Kind != PlanExecuteRecoverySurfaceAnchorRefresh {
		t.Fatalf("surface kind = %q, want anchor_refresh; state=%#v", surface.Kind, surface.State)
	}

	assertEqualSet(t, surface.State.CallableTools, []string{"read_file", "code_navigate"})
	assertEqualSet(t, surface.State.TerminalResponseShapes, nil)

	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief:             brief,
		Results:           results,
		BaseAllowedTools:  brief.AllowedTools,
		TextualToolParser: true,
	})
	if contract.PhaseID != turnPhaseStaleAnchorRefresh {
		t.Fatalf("PhaseID = %q, want %q", contract.PhaseID, turnPhaseStaleAnchorRefresh)
	}

	assertEqualSet(t, contract.ActionPolicy.TerminalResponseShapes, nil)
	assertNotContains(t, contract.ActionPolicy.AllowedActionClasses, actionSourceSatisfied)
	assertNotContains(t, contract.ActionPolicy.AllowedActionClasses, actionBlocker)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertNotContains(t, contract.ActionPolicy.AllowedActionClasses, actionSubmitPlan)
}

func TestDeriveTurnPhaseContractSourceSatisfiedTerminalSurface(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		PermissionProfile: "guarded_workspace_write",
		AllowedTools:      []string{"read_file", "code_navigate", "submit_plan"},
		RequiredEditPaths: []string{"a.go"},
	}
	results := []workspace.ToolResult{
		{Tool: "read_file", Summary: "Read f1 a.go lines 1-10 (total lines: 10)", Output: "1:package a\n"},
		{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: source-satisfied terminal shape required",
			Error:   "submit_plan cannot carry a source-satisfied/no-op closeout. Answer `Decision: source-satisfied` with per-path current-state evidence, submit real edits, or answer <<QUESTION>> with the exact blocker.",
			Code:    workspace.SubmitPlanSourceSatisfiedTerminalRequiredCode,
		},
	}

	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief:             brief,
		Results:           results,
		BaseAllowedTools:  brief.AllowedTools,
		TextualToolParser: true,
	})

	if contract.PhaseID != turnPhaseSourceSatisfiedTerminal {
		t.Fatalf("PhaseID = %q, want %q", contract.PhaseID, turnPhaseSourceSatisfiedTerminal)
	}

	if len(contract.AllowedTools) != 0 || len(contract.ActionPolicy.CallableToolNames) != 0 {
		t.Fatalf("source-satisfied terminal surface must expose no tools, got allowed=%#v callable=%#v", contract.AllowedTools, contract.ActionPolicy.CallableToolNames)
	}

	assertEqualSet(t, contract.ActionPolicy.AllowedActionClasses, []string{actionSourceSatisfied, actionBlocker})
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionSubmitPlan)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionRead)
	assertEqualSet(t, contract.ActionPolicy.TerminalResponseShapes, []string{"Decision: source-satisfied", "<<QUESTION>>:"})
	assertEqualSet(t, contract.RuntimeCall.ExpressibleActionClasses, []string{actionSourceSatisfied, actionBlocker})

	if contract.RuntimeCall.ParserContract != "source_satisfied_terminal" {
		t.Fatalf("ParserContract = %q, want source_satisfied_terminal", contract.RuntimeCall.ParserContract)
	}
}

func TestDeriveTurnPhaseContractGroundingRejectedPlanUsesSourceRefresh(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "code_navigate", "search_content", "submit_plan"},
		},
		Results: []workspace.ToolResult{
			{Tool: "read_file", Summary: "Read f1 cmd/idiom/product_workflow.go lines 1-120 (total lines: 2894)"},
			{
				Tool:    "submit_plan",
				Summary: "submit_plan rejected: edit paths lack source grounding",
				Error:   "submit_plan edits for plan-execute planner atoms must be grounded before executor handoff; missing source-body evidence for `internal/workflows/productworkflow/tdd.go`. Read each existing edit path with `read_file` or `code_navigate` `read_symbol`/`extract_body`, then submit a corrected plan.",
			},
		},
		TextualToolParser: true,
	})

	if contract.PhaseID != turnPhaseStaleAnchorRefresh {
		t.Fatalf("PhaseID = %q, want %q", contract.PhaseID, turnPhaseStaleAnchorRefresh)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionSubmitPlan)

	if len(contract.AllowedTools) != 0 || len(contract.ActionPolicy.CallableToolNames) != 0 {
		t.Fatalf("textual source refresh must not advertise manifest tools, got allowed=%#v callable=%#v", contract.AllowedTools, contract.ActionPolicy.CallableToolNames)
	}

	if contract.RuntimeCall.ParserContract != "source_refresh" {
		t.Fatalf("ParserContract = %q, want source_refresh", contract.RuntimeCall.ParserContract)
	}
}

func TestDeriveTurnPhaseContractSubmitCorrectionAfterAnchorRefresh(t *testing.T) {
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
				Error:   "submit_plan must use edits[] for changed required paths and top-level source_satisfied_paths for already-final required paths; invalid edit anchor(s): `a.go: old_text not found in current source`.",
			},
			{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "route_summary: Read Target in a.go lines 1-10\n1:package a"},
		},
		TextualToolParser: true,
	})

	if contract.PhaseID != turnPhaseSubmitReadyTextual {
		t.Fatalf("PhaseID = %q, want %q", contract.PhaseID, turnPhaseSubmitReadyTextual)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionSubmitPlan)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionRead)

	if contract.DerivationReason != "plan_execute_submit_plan_correction" {
		t.Fatalf("DerivationReason = %q, want plan_execute_submit_plan_correction", contract.DerivationReason)
	}
}

func TestDeriveTurnPhaseContractSubmitCorrectionIsSubmitPlanOnly(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "code_navigate", "search_content", "submit_plan"},
			RequiredEditPaths: []string{"a.go", "b.go"},
		},
		Results: []workspace.ToolResult{
			{Tool: "read_file", Summary: "Read f1 a.go lines 1-10 (total lines: 10)"},
			{Tool: "read_file", Summary: "Read f2 b.go lines 1-10 (total lines: 10)"},
			{
				Tool:    "submit_plan",
				Summary: "submit_plan rejected: required edit path coverage mismatch",
				Error:   "submit_plan must use edits[] for changed required paths and top-level source_satisfied_paths for already-final required paths; missing required edit path(s): `b.go`.",
			},
		},
		TextualToolParser: true,
	})

	if contract.PhaseID != turnPhaseSubmitReadyTextual {
		t.Fatalf("PhaseID = %q, want %q", contract.PhaseID, turnPhaseSubmitReadyTextual)
	}

	if contract.DerivationReason != "plan_execute_submit_plan_correction" {
		t.Fatalf("DerivationReason = %q, want plan_execute_submit_plan_correction", contract.DerivationReason)
	}

	assertEqualSet(t, contract.TerminalOutcomes, []string{"submit_plan"})
	assertEqualSet(t, contract.ActionPolicy.AllowedActionClasses, []string{actionSubmitPlan})
	assertEqualSet(t, contract.ActionPolicy.TerminalResponseShapes, []string{"submit_plan"})
	assertEqualSet(t, contract.RuntimeCall.ExpressibleActionClasses, []string{actionSubmitPlan})
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionSourceSatisfied)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionBlocker)
	assertNotContains(t, contract.ActionPolicy.AllowedActionClasses, actionSourceSatisfied)
	assertNotContains(t, contract.ActionPolicy.AllowedActionClasses, actionBlocker)
}

func TestTurnPhaseContractInvalidActionClassesWinDuringNormalization(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"submit_plan"},
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
		TextualToolParser: true,
	})

	contract.ActionPolicy.AllowedActionClasses = []string{actionSubmitPlan, actionBlocker}
	contract.ActionPolicy.InvalidActionClasses = []string{actionBlocker}
	contract.RuntimeCall.ExpressibleActionClasses = []string{actionSubmitPlan, actionBlocker}

	contract.ActionPolicy.AllowedActionClasses = actionClassesAllowedByPolicy(contract.ActionPolicy.AllowedActionClasses, contract.ActionPolicy.InvalidActionClasses)
	contract.RuntimeCall.ExpressibleActionClasses = actionClassesAllowedByPolicy(contract.RuntimeCall.ExpressibleActionClasses, contract.ActionPolicy.InvalidActionClasses)

	assertEqualSet(t, contract.ActionPolicy.AllowedActionClasses, []string{actionSubmitPlan})
	assertEqualSet(t, contract.RuntimeCall.ExpressibleActionClasses, []string{actionSubmitPlan})
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionBlocker)
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
				OpenObligations: []string{"source_sanity_failed:internal/contracts/writerroute/decision_test.go"},
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

func TestDeriveTurnPhaseContractReviewRepairCompletionGateStartsTerminal(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			PermissionProfile: "plan_submission_review",
			AllowedTools:      []string{"read_file", "code_navigate", "submit_plan"},
		},
		Results: []workspace.ToolResult{
			{
				Tool:    "completion_gate",
				Summary: "Review-repair completion gate rejected final answer",
				Error:   "review-repair must close with exactly one terminal outcome",
			},
		},
	})

	if contract.PhaseID != turnPhaseReviewRepairTerminal {
		t.Fatalf("PhaseID = %q, want %q", contract.PhaseID, turnPhaseReviewRepairTerminal)
	}

	assertEqualSet(t, contract.AllowedTools, []string{"submit_plan"})
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionReviewPass)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionSubmitPlan)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionBlocker)
	assertContains(t, contract.TerminalOutcomes, "Decision: pass")
	assertContains(t, contract.TerminalOutcomes, "submit_plan")
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionSearch)
	assertNotContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertNotContains(t, contract.ActionPolicy.AllowedActionClasses, actionSearch)
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

func TestDeriveTurnPhaseContractWriteReadyDropsSupersededPendingRedirect(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			Objective:         "fix the session pane cursor scrolling",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "code_navigate", "search_content", "edit_file", "apply_patch"},
			TargetPacket: []string{
				"candidate file: internal/surfaces/tui/app/implementation_dispatch.go",
				"candidate file: internal/surfaces/tui/app/sessions_panel.go",
			},
		},
		Results: []workspace.ToolResult{
			{
				Tool:    "read_file",
				Summary: "Redirected internal/surfaces/tui/app/implementation_dispatch.go to code_navigate",
				Output:  "Use code_navigate for source-body evidence in internal/surfaces/tui/app/implementation_dispatch.go.",
			},
			{
				Tool:    "read_file",
				Summary: "Read f1 internal/surfaces/tui/app/sessions_panel.go lines 1-219 (total lines: 219)",
				Output:  "func renderSessionsPanelContent() {}\nfunc handleSessionsPanelKey() {}\n",
			},
		},
		BaseAllowedTools: []string{"read_file", "code_navigate", "search_content", "edit_file", "apply_patch"},
	})

	if contract.PhaseID != turnPhaseImplementationWriteReady {
		t.Fatalf("PhaseID = %q, want %q", contract.PhaseID, turnPhaseImplementationWriteReady)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionPerEditWrite)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.AllowedTools, "read_file")
	assertContains(t, contract.AllowedTools, "search_content")
	assertNotContains(t, contract.AllowedTools, "code_navigate")

	joined := strings.Join(append(append([]string{}, contract.RequiredNext...), contract.InvalidNext...), "\n")
	if strings.Contains(joined, "pending redirected source-body obligation") ||
		strings.Contains(joined, "implementation_dispatch.go via code_navigate") {

		t.Fatalf("stale redirected candidate should not remain a hard write-ready obligation: %#v / %#v", contract.RequiredNext, contract.InvalidNext)
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
			RequiredEditPaths: []string{"internal/surfaces/tui/app/model_swap.go"},
			AllowedTools:      []string{"read_file", "code_navigate", "search_content", "edit_file", "source_sanity", "shell", "git_diff", "git_status"},
		},
		Results: []workspace.ToolResult{{
			Tool:    "edit_file",
			Summary: "Edited internal/surfaces/tui/app/model_swap.go",
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

func TestDeriveTurnPhaseContractImplementationTextualDoesNotAdvertiseManifestTools(t *testing.T) {
	tests := []struct {
		name           string
		input          TurnPhaseContractInput
		wantPhaseID    string
		wantAction     string
		wantHiddenTool string
	}{
		{
			name: "target source",
			input: TurnPhaseContractInput{
				Brief: workerruntime.WorkerRuntimeBrief{
					TaskFamily:        "implementation",
					Contract:          "implementation_result",
					PermissionProfile: "guarded_workspace_write",
					AllowedTools:      []string{"context_pack", "search_content", "read_file", "code_navigate", "edit_file", "apply_patch"},
				},
				TextualToolParser: true,
				ToolTransportMode: "textual",
			},
			wantPhaseID:    turnPhaseImplementationTargetSource,
			wantAction:     actionRead,
			wantHiddenTool: "read_file",
		},
		{
			name: "write ready",
			input: TurnPhaseContractInput{
				Brief: workerruntime.WorkerRuntimeBrief{
					TaskFamily:        "implementation",
					Contract:          "implementation_result",
					PermissionProfile: "guarded_workspace_write",
					AllowedTools:      []string{"read_file", "edit_file", "apply_patch", "shell"},
					RequiredEditPaths: []string{"pkg/feature.go"},
				},
				TextualToolParser: true,
				ToolTransportMode: "textual",
			},
			wantPhaseID:    turnPhaseImplementationWriteReady,
			wantAction:     actionPerEditWrite,
			wantHiddenTool: "edit_file",
		},
		{
			name: "verify ready",
			input: TurnPhaseContractInput{
				Brief: workerruntime.WorkerRuntimeBrief{
					TaskFamily:        "implementation",
					Contract:          "implementation_result",
					PermissionProfile: "guarded_workspace_write",
					AllowedTools:      []string{"read_file", "edit_file", "source_sanity", "shell", "git_diff"},
				},
				Results: []workspace.ToolResult{{
					Tool:    "edit_file",
					Summary: "Edited pkg/feature.go",
				}},
				TextualToolParser: true,
				ToolTransportMode: "textual",
			},
			wantPhaseID:    turnPhaseImplementationVerifyReady,
			wantAction:     actionVerify,
			wantHiddenTool: "source_sanity",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			contract := deriveTurnPhaseContract(tt.input)
			if contract.PhaseID != tt.wantPhaseID {
				t.Fatalf("PhaseID = %q, want %q", contract.PhaseID, tt.wantPhaseID)
			}

			if len(contract.AllowedTools) != 0 || len(contract.ActionPolicy.CallableToolNames) != 0 {
				t.Fatalf("textual implementation phase must not advertise manifest tools, got allowed=%#v callable=%#v", contract.AllowedTools, contract.ActionPolicy.CallableToolNames)
			}

			assertContains(t, contract.HiddenTools, tt.wantHiddenTool)
			assertContains(t, contract.ActionPolicy.AllowedActionClasses, tt.wantAction)

			if issues := autonomy.ReconcileTurnPhaseSurface(
				contract.autonomyEventPayload(),
				autonomy.SurfaceSummaryFromTurnPhaseContract(contract.autonomyEventPayload()),
			); len(issues) != 0 {
				t.Fatalf("textual implementation self-summary should reconcile cleanly, got %#v", issues)
			}
		})
	}
}

func TestDeriveTurnPhaseContractGenericTextualDoesNotAdvertiseManifestTools(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "debug_logs",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"read_file", "search_content"},
			RequiredNext:      []string{"inspect the concrete log artifact or answer from existing evidence"},
		},
		TextualToolParser: true,
		ToolTransportMode: "textual",
	})

	if contract.PhaseID != turnPhaseGeneric {
		t.Fatalf("PhaseID = %q, want %q", contract.PhaseID, turnPhaseGeneric)
	}

	if len(contract.AllowedTools) != 0 || len(contract.ActionPolicy.CallableToolNames) != 0 {
		t.Fatalf("textual generic phase must not advertise manifest tools, got allowed=%#v callable=%#v", contract.AllowedTools, contract.ActionPolicy.CallableToolNames)
	}

	assertContains(t, contract.HiddenTools, "read_file")
	assertContains(t, contract.RuntimeCall.ExpressibleActionClasses, actionFinalAnswer)

	if issues := autonomy.ReconcileTurnPhaseSurface(
		contract.autonomyEventPayload(),
		autonomy.SurfaceSummaryFromTurnPhaseContract(contract.autonomyEventPayload()),
	); len(issues) != 0 {
		t.Fatalf("textual generic self-summary should reconcile cleanly, got %#v", issues)
	}
}

func TestDeriveTurnPhaseContractVerificationOnlyShellAllowsVerify(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "verification_only",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_verification_shell",
			AllowedTools:      []string{"shell"},
			RequiredNext:      []string{"first tool call must run one verification command with shell"},
		},
	})

	if contract.PhaseID != turnPhaseGeneric {
		t.Fatalf("PhaseID = %q, want %q", contract.PhaseID, turnPhaseGeneric)
	}

	assertEqualSet(t, contract.AllowedTools, []string{"shell"})
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionVerify)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionBlocker)
	assertContains(t, contract.RuntimeCall.ExpressibleActionClasses, actionVerify)

	if issues := autonomy.ReconcileTurnPhaseSurface(
		contract.autonomyEventPayload(),
		autonomy.TurnPhaseSurfaceSummaryEvent{
			SchemaVersion:           autonomy.TurnPhaseSurfaceSummarySchemaVersion,
			SurfaceID:               "next_action_state",
			ContractID:              contract.ContractID,
			PhaseID:                 contract.PhaseID,
			AdvertisedActionClasses: []string{actionVerify, actionBlocker},
		},
	); len(issues) != 0 {
		t.Fatalf("verification-only shell surface should reconcile cleanly, got %#v", issues)
	}
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

func TestDeriveTurnPhaseContractFailedVerificationOpensRepairLane(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "code_navigate", "search_content", "edit_file", "apply_patch", "source_sanity", "shell", "git_diff", "git_status"},
		},
		Results: []workspace.ToolResult{
			{Tool: "edit_file", Summary: "Edited pkg/feature.go"},
			{Tool: "shell", Summary: "Ran shell command", Output: "$ npm run build\n", Error: "build failed"},
		},
	})

	if contract.Phase != "verification_repair" {
		t.Fatalf("Phase = %q, want verification_repair", contract.Phase)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionPerEditWrite)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionVerify)
	assertContains(t, contract.AllowedTools, "edit_file")
	assertContains(t, contract.AllowedTools, "read_file")
	assertContains(t, contract.AllowedTools, "shell")
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionFinalAnswer)
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

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionSearch)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionPerEditWrite)
	assertContains(t, contract.AllowedTools, "edit_file")
	assertContains(t, contract.AllowedTools, "apply_patch")
	assertContains(t, contract.AllowedTools, "read_file")
	assertContains(t, contract.AllowedTools, "search_content")

	if !strings.Contains(strings.Join(contract.Constraints, "\n"), "bounded_source_refresh_allowance") {
		t.Fatalf("target-write-ready contract should project bounded source refresh allowance: %#v", contract.Constraints)
	}
}

func TestDeriveTurnPhaseContractTargetWriteReadyShowsPendingRedirectedSourceObligation(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			Objective:         "fix session panel scrolling",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "code_navigate", "search_content", "edit_file", "apply_patch"},
		},
		Results: []workspace.ToolResult{
			{
				Tool:    "read_file",
				Summary: "Read f1 internal/surfaces/tui/app/sessions_panel.go lines 1-20 (total lines: 80)",
				Output:  "package app\n\nfunc PanelVisible() bool {\n\treturn true\n}\n",
			},
			{
				Tool:    "read_file",
				Summary: "Redirected internal/surfaces/tui/app/sessions.go to code_navigate",
				Output:  "Use code_navigate for source-body evidence in internal/surfaces/tui/app/sessions.go.",
			},
		},
	})

	if contract.Phase != "target_write_ready" {
		t.Fatalf("Phase = %q, want target_write_ready with pending redirected source obligation; contract=%#v", contract.Phase, contract)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionPerEditWrite)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionBlocker)
	assertContains(t, contract.AllowedTools, "code_navigate")
	assertContains(t, contract.ActionPolicy.CallableToolNames, "code_navigate")
	assertNotContains(t, contract.AllowedTools, "read_file")
	assertNotContains(t, contract.AllowedTools, "search_content")
	assertNotContains(t, contract.ActionPolicy.AllowedActionClasses, actionSearch)

	joinedNext := strings.Join(contract.RequiredNext, "\n")
	for _, want := range []string{"internal/surfaces/tui/app/sessions.go", "code_navigate", "source-body", "capability=read_symbol", "pattern/symbol"} {
		if !strings.Contains(joinedNext, want) {
			t.Fatalf("RequiredNext missing pending redirected source obligation detail %q: %#v", want, contract.RequiredNext)
		}
	}
}

func TestDeriveTurnPhaseContractUnscopedImplementationDesignDocReadStaysTargetSource(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "context_pack", "search_content", "edit_file", "apply_patch"},
		},
		Results: []workspace.ToolResult{{
			Tool:    "read_file",
			Summary: "Read f1 docs/design-tdd-coverage.md lines 1-200 (total lines: 815)",
			Output:  "Phase 1: EARS And BDD Coverage Checks\n",
		}},
	})

	if contract.PhaseID != turnPhaseImplementationTargetSource {
		t.Fatalf("PhaseID = %q, want target source after design-doc-only read", contract.PhaseID)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionSearch)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionPerEditWrite)
	assertContains(t, contract.AllowedTools, "read_file")
	assertContains(t, contract.AllowedTools, "context_pack")
	assertContains(t, contract.AllowedTools, "edit_file")
}

func TestDeriveTurnPhaseContractUnscopedImplementationWrongTargetReadStaysTargetSource(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			Objective:         "the session panel cursor should scroll down when navigating with arrow keys",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "context_pack", "code_navigate", "search_content", "edit_file", "apply_patch"},
			TargetPacket: []string{
				"### Candidate Files",
				"- `internal/surfaces/tui/app/app.go` (read_only_evidence): weak packet candidate",
				"- `internal/runtime/agent/phase_contract.go` (editable): matched task terms",
				"- `internal/runtime/agent/runner.go` (editable): matched task terms",
			},
		},
		Results: []workspace.ToolResult{{
			Tool:    "read_file",
			Summary: "Read f1 internal/surfaces/tui/app/app.go lines 1-55 (total lines: 55)",
			Output:  "package app\n\ntype Model struct{}\n",
		}},
	})

	if contract.PhaseID != turnPhaseImplementationTargetSource {
		t.Fatalf("PhaseID = %q, want target_source after non-plausible source read; contract=%#v", contract.PhaseID, contract)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionSearch)
	assertContains(t, contract.AllowedTools, "context_pack")
	assertContains(t, contract.AllowedTools, "read_file")
}

func TestDeriveTurnPhaseContractSingleObjectiveTokenReadStaysTargetSource(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			Objective:         "The history tab's shortened text carries newlines that do not make sense for wrapping.",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "context_pack", "code_navigate", "search_content", "edit_file", "apply_patch"},
			TargetPacket: []string{
				"### Candidate Files",
				"- `internal/runtime/agent/phase_contract.go` (editable): matched task terms",
				"- `cmd/idiom/lane_history.go` (read_only_evidence): related by calls relationship",
			},
		},
		Results: []workspace.ToolResult{{
			Tool:    "read_file",
			Summary: "Read f1 cmd/idiom/lane_history.go lines 1-331 (total lines: 331)",
			Output:  "package main\n\ntype laneHistoryEvent struct{}\n",
		}},
	})

	if contract.PhaseID != turnPhaseImplementationTargetSource {
		t.Fatalf("PhaseID = %q, want target_source after weak single-token evidence read; contract=%#v", contract.PhaseID, contract)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionSearch)
	assertContains(t, contract.AllowedTools, "context_pack")
	assertContains(t, contract.AllowedTools, "search_content")
}

func TestDeriveTurnPhaseContractNonEditableTargetPacketReadStaysTargetSource(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			Objective:         "the session panel cursor should scroll down when navigating with arrow keys",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "context_pack", "code_navigate", "search_content", "edit_file", "apply_patch"},
			TargetPacket: []string{
				"### Candidate Files",
				"- `internal/surfaces/tui/app/model_state_test.go` (test): related coverage only",
			},
		},
		Results: []workspace.ToolResult{{
			Tool:    "read_file",
			Summary: "Read f1 internal/surfaces/tui/app/model_state_test.go lines 1-80 (total lines: 80)",
			Output:  "package app\n\nfunc TestModelState(t *testing.T) {}\n",
		}},
	})

	if contract.PhaseID != turnPhaseImplementationTargetSource {
		t.Fatalf("PhaseID = %q, want target_source after non-editable target-packet read; contract=%#v", contract.PhaseID, contract)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.AllowedTools, "context_pack")
}

func TestDeriveTurnPhaseContractUnscopedImplementationPlausibleTargetReadBecomesWriteReady(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			Objective:         "the session panel cursor should scroll down when navigating with arrow keys",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "context_pack", "code_navigate", "search_content", "edit_file", "apply_patch"},
			TargetPacket: []string{
				"### Candidate Files",
				"- `internal/runtime/agent/phase_contract.go` (editable): matched task terms",
				"- `internal/runtime/agent/runner.go` (editable): matched task terms",
			},
		},
		Results: []workspace.ToolResult{{
			Tool:    "read_file",
			Summary: "Read f2 internal/surfaces/tui/app/sessions_panel.go lines 1-80 (total lines: 320)",
			Output:  "package app\n\ntype sessionsPanelModel struct{}\n",
		}},
	})

	if contract.Phase != "target_write_ready" {
		t.Fatalf("Phase = %q, want target_write_ready for objective-plausible source read; contract=%#v", contract.Phase, contract)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionPerEditWrite)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.AllowedTools, "edit_file")
	assertContains(t, contract.AllowedTools, "read_file")
	assertContains(t, contract.AllowedTools, "search_content")
	assertNotContains(t, contract.AllowedTools, "context_pack")
}

func TestDeriveTurnPhaseContractUnscopedImplementationStaysWriteReadyAfterMultipleSourceReads(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "code_navigate", "search_content", "edit_file", "apply_patch", "source_sanity"},
		},
		Results: []workspace.ToolResult{
			{
				Tool:    "read_file",
				Summary: "Read f1 docs/design-tdd-coverage.md lines 1-20 (total lines: 100)",
				Output:  "Phase 1: EARS And BDD Coverage Checks\n",
			},
			{
				Tool:    "read_file",
				Summary: "Read f2 internal/workflows/productworkflow/coverage_gate_test.go lines 1-20 (total lines: 100)",
				Output:  "func TestCoverageGate(t *testing.T) {}\n",
			},
		},
	})

	if contract.Phase != "target_write_ready" {
		t.Fatalf("Phase = %q, want target_write_ready", contract.Phase)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionPerEditWrite)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.AllowedTools, "edit_file")
	assertContains(t, contract.AllowedTools, "read_file")
	assertContains(t, contract.AllowedTools, "search_content")
	assertNotContains(t, contract.AllowedTools, "code_navigate")
}

func TestTurnPhaseSurfaceSummaryDoesNotClassifyParserReadableWriteAsRead(t *testing.T) {
	classes := turnPhaseSurfaceActionClassesFromText([]string{
		"parser-readable write to the sourced target path, or <<QUESTION>> with the exact edit blocker/no-op evidence",
	})

	assertContains(t, classes, actionPerEditWrite)
	assertContains(t, classes, actionBlocker)
	assertNotContains(t, classes, actionRead)
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

func TestDeriveTurnPhaseContractUnscopedImplementationStaysTargetSourceAfterCandidateAnchorMiss(t *testing.T) {
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
			Output: strings.Join([]string{
				"capability: read_symbol",
				"language: go",
				"routed_tool: go_read_symbol",
				`route_summary: Go symbol read path-filter miss; found 1 candidate anchor(s) for "Model.Update"`,
				"",
				"requested_symbol: Model.Update",
				"requested_path: internal/surfaces/tui/app/model_state.go",
				"path_filter_status: no source-body match in requested path",
				"candidate_anchor_status: anchors only; read one candidate path to inspect source body",
				"candidate_anchors:",
				"- internal/surfaces/tui/app/lifecycle.go:409 method Model.Update",
			}, "\n"),
		}},
	})

	if contract.PhaseID != turnPhaseImplementationTargetSource {
		t.Fatalf("PhaseID = %q, want target_source after candidate-anchor miss; contract=%#v", contract.PhaseID, contract)
	}

	assertContains(t, contract.AllowedTools, "read_file")
	assertContains(t, contract.AllowedTools, "code_navigate")
}

func TestDeriveTurnPhaseContractUnscopedImplementationWriteReadyAfterSymbolBody(t *testing.T) {
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
			Output: strings.Join([]string{
				"capability: read_symbol",
				"language: go",
				"routed_tool: go_read_symbol",
				"route_summary: Read EvaluateCoverageGate in internal/workflows/productworkflow/coverage_gate.go lines 1-8",
				"1:func EvaluateCoverageGate() bool {",
				"2:\treturn true",
				"3:}",
			}, "\n"),
		}},
	})

	if contract.Phase != "target_write_ready" {
		t.Fatalf("Phase = %q, want target_write_ready after symbol body; contract=%#v", contract.Phase, contract)
	}

	assertContains(t, contract.AllowedTools, "edit_file")
	assertContains(t, contract.AllowedTools, "apply_patch")
	assertContains(t, contract.AllowedTools, "read_file")
	assertContains(t, contract.AllowedTools, "search_content")
	assertNotContains(t, contract.AllowedTools, "code_navigate")
}

func TestDeriveTurnPhaseContractReadOnlyWorkspaceAllowsFinalAnswerStrategyWhileEvidencePending(t *testing.T) {
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
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionFinalAnswer)
	assertNotContains(t, contract.ActionPolicy.InvalidActionClasses, actionFinalAnswer)
	assertContains(t, contract.TerminalOutcomes, "final answer")
	assertContains(t, contract.RequiredNext, "final answer from gathered evidence or explicit uncertainty, one bounded read/search for an exact unsupported claim, or <<QUESTION>> with the exact blocker")
	assertContains(t, contract.AllowedTools, "read_file")
	assertContains(t, contract.AllowedTools, "search_content")
	assertNotContains(t, contract.AllowedTools, "edit_file")
	assertNotContains(t, contract.AllowedTools, "shell")
}

func TestDeriveTurnPhaseContractReadOnlyConversationWithoutReadinessRequiresEvidence(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"search_content", "read_file", "edit_file"},
		},
	})

	if contract.Phase != "read_only_evidence_pending" {
		t.Fatalf("Phase = %q, want read_only_evidence_pending", contract.Phase)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionSearch)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionFinalAnswer)
	assertNotContains(t, contract.ActionPolicy.InvalidActionClasses, actionFinalAnswer)
	assertContains(t, contract.AllowedTools, "read_file")
	assertContains(t, contract.AllowedTools, "search_content")
	assertNotContains(t, contract.AllowedTools, "edit_file")
}

func TestDeriveTurnPhaseContractReadOnlyDirectAnswerWithoutReadinessRequiresEvidence(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "direct_answer",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"search_content", "read_file", "edit_file"},
		},
	})

	if contract.Phase != "read_only_evidence_pending" {
		t.Fatalf("Phase = %q, want read_only_evidence_pending", contract.Phase)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionSearch)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionFinalAnswer)
	assertNotContains(t, contract.ActionPolicy.InvalidActionClasses, actionFinalAnswer)
	assertContains(t, contract.AllowedTools, "read_file")
	assertContains(t, contract.AllowedTools, "search_content")
	assertNotContains(t, contract.AllowedTools, "edit_file")
}

func TestDeriveTurnPhaseContractReadOnlyWorkspaceAllowsAnswerAfterEvidence(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "direct_answer",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			Objective:         "Explain ShortLabel behavior in internal/labels/labels.go.",
			AllowedTools:      []string{"search_content", "read_file"},
			Readiness:         &workerruntime.ReadinessState{ReadyToAnswer: false, OpenObligations: []string{"evidence"}},
		},
		Results: []workspace.ToolResult{{
			Tool:    "read_file",
			Summary: "Read f1 internal/labels/labels.go lines 1-5 (total lines: 5)",
			Output:  "1:package labels\n2:\n3:func ShortLabel() string {\n4:\treturn \"Save changes\"\n5:}\n",
		}},
	})

	if contract.Phase != "read_only_evidence_available" {
		t.Fatalf("Phase = %q, want read_only_evidence_available after evidence", contract.Phase)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionFinalAnswer)
	assertNotContains(t, contract.ActionPolicy.InvalidActionClasses, actionFinalAnswer)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionGenericProbe)
	assertNotContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertNotContains(t, contract.ActionPolicy.AllowedActionClasses, actionSearch)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionSearch)
}

func TestDeriveTurnPhaseContractReadOnlyConversationEvidenceAvailableKeepsProofReadSurface(t *testing.T) {
	input := TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			Objective:         "What does internal/labels/labels.go return?",
			AllowedTools:      []string{"search_content", "read_file", "code_navigate"},
		},
		Results: []workspace.ToolResult{
			{
				Tool:    "read_file",
				Summary: "Read f1 internal/labels/labels.go lines 1-5",
				Output:  "package labels\nfunc ShortLabel() string { return \"Save changes\" }\n",
			},
			{Tool: "code_navigate", Summary: "Code navigation routed references via go_references"},
		},
	}
	contract := deriveTurnPhaseContract(input)

	if contract.Phase != "read_only_evidence_available" {
		t.Fatalf("Phase = %q, want read_only_evidence_available", contract.Phase)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionFinalAnswer)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionBlocker)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionGenericProbe)
	assertNotContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertNotContains(t, contract.ActionPolicy.AllowedActionClasses, actionSearch)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.InvalidActionClasses, actionSearch)
	assertEqualSet(t, contract.AllowedTools, []string{"search_content", "read_file", "code_navigate", "task"})
	assertEqualSet(t, contract.ActionPolicy.CallableToolNames, []string{"search_content", "read_file", "code_navigate", "task"})

	input.TextualToolParser = true
	input.ToolTransportMode = "textual"

	textual := deriveTurnPhaseContract(input)

	if textual.Phase != "read_only_evidence_available" {
		t.Fatalf("textual Phase = %q, want read_only_evidence_available", textual.Phase)
	}

	assertContains(t, textual.ActionPolicy.AllowedActionClasses, actionGenericProbe)
	assertNotContains(t, textual.ActionPolicy.AllowedActionClasses, actionRead)
	assertNotContains(t, textual.ActionPolicy.AllowedActionClasses, actionSearch)
	assertContains(t, textual.ActionPolicy.InvalidActionClasses, actionRead)
	assertContains(t, textual.ActionPolicy.InvalidActionClasses, actionSearch)
	assertEqualSet(t, textual.AllowedTools, []string{"search_content", "read_file", "code_navigate", "task"})
	assertEqualSet(t, textual.ActionPolicy.CallableToolNames, []string{"search_content", "read_file", "code_navigate", "task"})
}

func TestDeriveTurnPhaseContractOmitsHiddenToolsFromCallableSurface(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "architecture",
			Contract:          "architecture_assessment",
			PermissionProfile: "read_only_workspace",
			Objective:         "Explain the architecture.",
			AllowedTools:      []string{"task", "search_content", "read_file", "code_navigate"},
		},
		BaseAllowedTools: []string{"task", "search_content", "read_file", "code_navigate"},
		HiddenTools:      []string{"task"},
		Results: []workspace.ToolResult{{
			Tool:    "read_file",
			Summary: "Read f1 internal/runtime/agent/runner.go lines 1-5",
			Output:  "package agent\nfunc RunExample() {}\n",
		}},
	})

	assertContains(t, contract.HiddenTools, "task")
	assertNotContains(t, contract.AllowedTools, "task")
	assertNotContains(t, contract.ActionPolicy.CallableToolNames, "task")
	assertNotContains(t, contract.ActionPolicy.AllowedActionClasses, actionGenericProbe)
	assertNotContains(t, contract.RuntimeCall.ExpressibleActionClasses, actionGenericProbe)
	assertContains(t, contract.ActionPolicy.CallableToolNames, "read_file")
}

func TestDeriveTurnPhaseContractReadOnlyConversationKeepsToolsForUnreadNamedPath(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "conversation",
			Contract:          "direct_answer",
			PermissionProfile: "read_only_workspace",
			Objective:         "Compare internal/labels/labels.go and internal/labels/labels_test.go.",
			AllowedTools:      []string{"search_content", "read_file", "code_navigate"},
			Readiness:         &workerruntime.ReadinessState{ReadyToAnswer: false, OpenObligations: []string{"evidence"}},
		},
		Results: []workspace.ToolResult{
			{Tool: "read_file", Summary: "Read f1 internal/labels/labels.go lines 1-5"},
		},
	})

	if contract.Phase != "read_only_evidence_pending" {
		t.Fatalf("Phase = %q, want read_only_evidence_pending until every named path is read", contract.Phase)
	}

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionRead)
	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionFinalAnswer)
	assertNotContains(t, contract.ActionPolicy.InvalidActionClasses, actionFinalAnswer)
	assertContains(t, contract.AllowedTools, "read_file")
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

	assertContains(t, contract.ActionPolicy.AllowedActionClasses, actionFinalAnswer)
	assertNotContains(t, contract.ActionPolicy.InvalidActionClasses, actionFinalAnswer)
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

func TestNextActionStateSurfaceSummaryReconcilesWithPhaseContract(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "submit_plan"},
			RequiredEditPaths: []string{"a.go"},
		},
		Results:           []workspace.ToolResult{{Tool: "read_file", Summary: "Read f1 a.go lines 1-10 (total lines: 10)"}},
		TextualToolParser: true,
	})
	state := applyTurnPhaseContractToNextActionState(runnerNextActionState{
		Phase: "generic",
		Step:  4,
	}, contract)

	state.NextValidActions = append([]string(nil), contract.TerminalOutcomes...)

	summary := turnPhaseSurfaceSummaryFromNextActionState(state, contract)
	issues := autonomy.ReconcileTurnPhaseSurface(contract.autonomyEventPayload(), summary)

	if len(issues) != 0 {
		t.Fatalf("next-action summary should reconcile cleanly, got %#v", issues)
	}

	invalidActionClass := actionPerEditWrite
	if !slices.Contains(contract.ActionPolicy.InvalidActionClasses, invalidActionClass) {
		t.Fatalf("expected %s to be invalid in submit-ready contract: %#v", invalidActionClass, contract.ActionPolicy)
	}

	state.NextValidActions = []string{phaseTestActionClassPrompt(invalidActionClass)}
	summary = turnPhaseSurfaceSummaryFromNextActionState(state, contract)

	if !slices.Contains(summary.AdvertisedActionClasses, invalidActionClass) {
		t.Fatalf("summary did not classify rendered invalid action %s: %#v", invalidActionClass, summary)
	}

	issues = autonomy.ReconcileTurnPhaseSurface(contract.autonomyEventPayload(), summary)
	if len(issues) == 0 {
		t.Fatalf("next-action summary should report rendered invalid %s guidance", invalidActionClass)
	}

	foundInvalid := false

	for _, issue := range issues {
		if issue.SurfacePair == "turn_phase_contract,next_action_state" &&
			issue.InvariantID == "turn_phase.action.invalid_advertised" &&
			issue.ActionClass == invalidActionClass {

			foundInvalid = true
		}
	}

	if !foundInvalid {
		t.Fatalf("expected invalid %s contradiction from rendered next-action state, got %#v", invalidActionClass, issues)
	}
}

func TestWriteReadyNextActionStateDoesNotMarkAllowedWriteInvalid(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "edit_file", "apply_patch"},
		},
		Results: []workspace.ToolResult{{
			Tool:    "read_file",
			Summary: "Read f1 pkg/feature.go lines 1-20 (total lines: 20)",
		}},
		TextualToolParser: true,
		ToolTransportMode: "textual",
	})
	if contract.PhaseID != turnPhaseImplementationWriteReady {
		t.Fatalf("PhaseID = %q, want %q", contract.PhaseID, turnPhaseImplementationWriteReady)
	}

	state := applyTurnPhaseContractToNextActionState(runnerNextActionState{
		Phase: "generic",
		Step:  4,
	}, contract)
	summary := turnPhaseSurfaceSummaryFromNextActionState(state, contract)

	if slices.Contains(summary.InvalidActionClasses, actionPerEditWrite) {
		t.Fatalf("next-action summary must not mark allowed write invalid, got %#v from invalid text %#v", summary.InvalidActionClasses, state.InvalidNextActions)
	}

	if issues := autonomy.ReconcileTurnPhaseSurface(contract.autonomyEventPayload(), summary); len(issues) != 0 {
		t.Fatalf("write-ready next-action summary should reconcile cleanly, got %#v", issues)
	}
}

func TestRequiredSourceSelfSurfaceSummaryReconcilesWithPhaseContract(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecutePlannerTaskFamily,
			PermissionProfile: "guarded_workspace_write",
			AllowedTools:      []string{"read_file", "code_navigate", "submit_plan"},
			RequiredEditPaths: []string{"a.go"},
		},
	})

	issues := autonomy.ReconcileTurnPhaseSurface(
		contract.autonomyEventPayload(),
		autonomy.SurfaceSummaryFromTurnPhaseContract(contract.autonomyEventPayload()),
	)
	if len(issues) != 0 {
		t.Fatalf("required-source self-summary should reconcile cleanly, got %#v", issues)
	}
}

func TestRecoveryCardSurfaceSummaryReconcilesWithPhaseContract(t *testing.T) {
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        PlanThenExecuteReviewRepairTaskFamily,
			PermissionProfile: "plan_submission_review",
			AllowedTools:      []string{"read_file", "search_content", "submit_plan"},
			Readiness: &workerruntime.ReadinessState{
				OpenObligations: []string{"source_sanity_failed:internal/example.go"},
			},
		},
	})
	card := BuildRecoveryCard(RecoveryCardInput{
		PhaseContract:       contract,
		CurrentAllowedTools: []string{"submit_plan"},
		RejectedRequest:     workspace.ToolRequest{Tool: "completion_gate"},
		RejectedResult: workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Review-repair pass rejected",
			Error:   "failed obligations remain",
		},
	})

	summary := card.TurnPhaseSurfaceSummary(contract)
	issues := autonomy.ReconcileTurnPhaseSurface(contract.autonomyEventPayload(), summary)

	if len(issues) != 0 {
		t.Fatalf("recovery-card summary should reconcile cleanly, got %#v", issues)
	}

	assertNotContains(t, card.CallableActionClasses, actionReviewPass)
	assertNotContains(t, card.TerminalOutcomes, "Decision: pass")
}

func assertContains(t *testing.T, values []string, want string) {
	t.Helper()

	if !slices.Contains(values, want) {
		t.Fatalf("expected %q in %#v", want, values)
	}
}

func phaseTestActionClassPrompt(actionClass string) string {
	switch actionClass {
	case actionRead:
		return "read another source file before submitting"
	case actionSearch:
		return "search the repository before submitting"
	case actionPerEditWrite:
		return "write another file before submitting"
	case actionVerify:
		return "run another verification command before submitting"
	case actionFinalAnswer:
		return "answer directly before submitting"
	default:
		return actionClass
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
