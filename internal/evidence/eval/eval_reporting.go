package eval

import (
	"slices"
	"strings"
)

func BuildSuggestedScenario(name string, records []Record) Scenario {
	metrics := Analyze(records)
	scenario := Scenario{
		Name:           strings.TrimSpace(name),
		PromptContains: nil,
		RequireTools:   suggestedTools(records),
		RequirePhases:  compactPhaseExpectations(metrics.Phases),
	}
	if strings.TrimSpace(metrics.LastUser) != "" {
		scenario.PromptContains = []string{metrics.LastUser}
	}
	switch {
	case metrics.VerificationPassed:
		scenario.RequireVerification = "passed"
	case metrics.VerificationFailed:
		scenario.RequireVerification = "failed"
	default:
		scenario.RequireVerification = "none"
	}
	if len(metrics.ChangedFiles) > 0 {
		scenario.RequireChangedFiles = append([]string(nil), metrics.ChangedFiles...)
	}
	if len(metrics.EvidencePaths) > 0 {
		scenario.RequireEvidencePaths = append([]string(nil), metrics.EvidencePaths...)
	}
	if len(metrics.LanguageCapabilities) > 0 {
		scenario.RequireLanguageCapabilities = compactLanguageCapabilityExpectations(metrics.LanguageCapabilities)
	}
	if len(metrics.VerificationPlans) > 0 {
		latest := metrics.VerificationPlans[len(metrics.VerificationPlans)-1]
		usesFullSuite := latest.UsesFullSuite
		scenario.RequireVerificationPlan = &VerificationPlanExpectation{
			Coverage:                latest.Coverage,
			Confidence:              latest.Confidence,
			RiskLevel:               latest.RiskLevel,
			UsesFullSuite:           &usesFullSuite,
			RequireCommands:         append([]string(nil), latest.Commands...),
			RequireExpectedFailures: append([]string(nil), latest.ExpectedFailures...),
		}
	}
	if len(metrics.ReadinessChecks) > 0 {
		latest := metrics.ReadinessChecks[len(metrics.ReadinessChecks)-1]
		ready := latest.Ready
		minEvidencePaths := len(latest.EvidencePaths)
		scenario.RequireReadiness = &ReadinessExpectation{
			Lane:             latest.Lane,
			Ready:            &ready,
			Confidence:       latest.Confidence,
			Action:           latest.Action,
			MinEvidencePaths: &minEvidencePaths,
		}
	}
	if len(metrics.DeepeningAttempts) > 0 {
		latest := metrics.DeepeningAttempts[len(metrics.DeepeningAttempts)-1]
		attempted := latest.Attempted
		exhausted := latest.Exhausted
		minEvidencePaths := len(latest.EvidencePaths)
		scenario.RequireDeepening = &DeepeningExpectation{
			Lane:             latest.Lane,
			Attempted:        &attempted,
			Exhausted:        &exhausted,
			Action:           latest.Action,
			MinEvidencePaths: &minEvidencePaths,
		}
	}
	if len(metrics.FinalAnswerCritics) > 0 {
		latest := metrics.FinalAnswerCritics[len(metrics.FinalAnswerCritics)-1]
		passed := latest.Passed
		repaired := latest.Repaired
		scenario.RequireFinalAnswerCritic = &FinalAnswerCriticExpectation{
			Contract: latest.Contract,
			Passed:   &passed,
			Action:   latest.Action,
			Repaired: &repaired,
		}
	}
	if len(metrics.EffortPolicies) > 0 {
		latest := metrics.EffortPolicies[len(metrics.EffortPolicies)-1]
		minAnalysis := latest.AnalysisToolBudget
		minImplement := latest.ImplementToolBudget
		minDeepening := latest.DeepeningPasses
		minMaxOutput := latest.MaxOutputTokens
		scenario.RequireEffortPolicy = &EffortPolicyExpectation{
			Lane:                   latest.Lane,
			Level:                  latest.Level,
			MinAnalysisToolBudget:  &minAnalysis,
			MinImplementToolBudget: &minImplement,
			MinDeepeningPasses:     &minDeepening,
			MinMaxOutputTokens:     &minMaxOutput,
			ConfidenceFloor:        latest.ConfidenceFloor,
			BudgetExhaustionMode:   latest.BudgetExhaustionMode,
		}
	}
	if len(metrics.LongHorizonStates) > 0 {
		latest := metrics.LongHorizonStates[len(metrics.LongHorizonStates)-1]
		stage := latest.Stage
		resumable := latest.Resumable
		scenario.RequireLongHorizonState = &LongHorizonStateExpectation{
			Action:                     latest.Action,
			MemoryKind:                 latest.MemoryKind,
			SourceLane:                 latest.SourceLane,
			Stage:                      &stage,
			FocusPath:                  latest.FocusPath,
			RequireRemaining:           append([]string(nil), latest.Remaining...),
			RequireOpenGaps:            append([]string(nil), latest.OpenGaps...),
			RequireAttemptedSearches:   append([]string(nil), latest.AttemptedSearches...),
			RequireChosenTargets:       append([]string(nil), latest.ChosenTargets...),
			RequireVerificationHistory: append([]string(nil), latest.VerificationHistory...),
			RequireNextActions:         append([]string(nil), latest.NextActions...),
			Resumable:                  &resumable,
		}
		if strings.TrimSpace(latest.NextStep) != "" {
			scenario.RequireLongHorizonState.NextContains = []string{latest.NextStep}
		}
	}
	if len(metrics.BudgetLedgers) > 0 {
		latest := metrics.BudgetLedgers[len(metrics.BudgetLedgers)-1]
		minModelCalls := latest.UsedModelCalls
		minEvidencePaths := latest.TotalEvidencePaths
		budgetExhausted := latest.BudgetExhausted
		scenario.RequireBudgetLedger = &BudgetLedgerExpectation{
			Lane:              latest.Lane,
			StopReason:        latest.StopReason,
			MinModelCalls:     &minModelCalls,
			MinEvidencePaths:  &minEvidencePaths,
			BudgetExhausted:   &budgetExhausted,
			UncertaintyPolicy: latest.UserVisibleUncertainty,
		}
	}
	if len(metrics.EvidenceDeltas) > 0 {
		latest := metrics.EvidenceDeltas[len(metrics.EvidenceDeltas)-1]
		minNewPaths := len(latest.NewEvidencePaths)
		minToolResults := latest.ToolResults
		maxFailedTools := latest.FailedTools
		scenario.RequireEvidenceDelta = &EvidenceDeltaExpectation{
			Lane:           latest.Lane,
			MinNewPaths:    &minNewPaths,
			MarginalValue:  latest.MarginalValue,
			MinToolResults: &minToolResults,
			MaxFailedTools: &maxFailedTools,
		}
	}
	if len(metrics.ClaimConfidences) > 0 {
		latest := metrics.ClaimConfidences[len(metrics.ClaimConfidences)-1]
		minEvidencePaths := len(latest.EvidencePaths)
		maxUnresolved := len(latest.UnresolvedUncertainty)
		scenario.RequireClaimConfidence = &ClaimConfidenceExpectation{
			Lane:             latest.Lane,
			Confidence:       latest.Confidence,
			MinEvidencePaths: &minEvidencePaths,
			MaxUnresolved:    &maxUnresolved,
			CalibrationBasis: latest.CalibrationBasis,
		}
	}
	if len(metrics.ContextCompactions) > 0 {
		latest := metrics.ContextCompactions[len(metrics.ContextCompactions)-1]
		minDropped := latest.DroppedMessages
		minRetained := latest.RetainedMessages
		maxHistory := latest.MaxHistory
		scenario.RequireContextCompaction = &ContextCompactionExpectation{
			MinDroppedMessages:  &minDropped,
			MinRetainedMessages: &minRetained,
			MaxHistory:          &maxHistory,
			RequirePinnedPaths:  append([]string(nil), latest.PinnedPaths...),
			PinnedMemoryKind:    latest.PinnedMemoryKind,
			StrategyContains:    nonEmptySingleton(latest.Strategy),
		}
	}
	if len(metrics.RouteAmbiguities) > 0 {
		latest := metrics.RouteAmbiguities[len(metrics.RouteAmbiguities)-1]
		ambiguous := latest.Ambiguous
		scenario.RequireRouteAmbiguity = &RouteAmbiguityExpectation{
			SelectedLane:     latest.SelectedLane,
			Ambiguous:        &ambiguous,
			RequireSignals:   append([]string(nil), latest.Signals...),
			TieBreakContains: nonEmptySingleton(latest.TieBreak),
		}
	}
	if len(metrics.VerificationFeedback) > 0 {
		latest := metrics.VerificationFeedback[len(metrics.VerificationFeedback)-1]
		scenario.RequireVerificationFeedback = &VerificationFeedbackExpectation{
			Confidence:             latest.Confidence,
			RequireFailedCommands:  append([]string(nil), latest.FailedCommands...),
			RequireFailureClasses:  append([]string(nil), latest.FailureClasses...),
			RecommendationContains: nonEmptySingleton(latest.LearnedRecommendation),
		}
	}
	if len(metrics.ToolFailureConfidence) > 0 {
		latest := metrics.ToolFailureConfidence[len(metrics.ToolFailureConfidence)-1]
		scenario.RequireToolFailureConfidence = &ToolFailureConfidenceExpectation{
			Lane:             latest.Lane,
			Tool:             latest.Tool,
			ConfidenceImpact: latest.ConfidenceImpact,
		}
	}
	if len(metrics.ControlRails) > 0 {
		latest := metrics.ControlRails[len(metrics.ControlRails)-1]
		failureToEval := latest.FailureToEval
		scenario.RequireControlRails = &ControlRailsExpectation{
			Lane:                  latest.Lane,
			FollowupLane:          latest.FollowupLane,
			Contract:              latest.Contract,
			AgentMode:             latest.AgentMode,
			WorkflowKind:          latest.WorkflowKind,
			WorkflowPhase:         latest.WorkflowPhase,
			TaskFamily:            latest.TaskFamily,
			PermissionProfile:     latest.PermissionProfile,
			RequireTargetRefs:     append([]string(nil), latest.TargetRefs...),
			RequireEvidence:       append([]string(nil), latest.RequiredEvidence...),
			RequireTools:          append([]string(nil), latest.RequiredTools...),
			RequireForbiddenTools: append([]string(nil), latest.ForbiddenTools...),
			RequireReadinessGate:  append([]string(nil), latest.ReadinessGate...),
			RequireSearchPolicy:   append([]string(nil), latest.SearchPolicy...),
			VerificationContract:  latest.VerificationContract,
			RequireStopConditions: append([]string(nil), latest.StopConditions...),
			RequireFinalCritic:    append([]string(nil), latest.FinalCritic...),
			FailureToEval:         &failureToEval,
		}
	}
	if len(metrics.RoutePlans) > 0 {
		latest := metrics.RoutePlans[len(metrics.RoutePlans)-1]
		scenario.RequireRoutePlan = &RoutePlanExpectation{
			InitialLane:   latest.InitialLane,
			FollowupLane:  latest.FollowupLane,
			Reason:        latest.Reason,
			Contract:      latest.Contract,
			AgentMode:     latest.AgentMode,
			WorkflowKind:  latest.WorkflowKind,
			WorkflowPhase: latest.WorkflowPhase,
			Thoroughness:  latest.Thoroughness,
		}
	}
	if len(metrics.WorkflowStates) > 0 {
		latest := metrics.WorkflowStates[len(metrics.WorkflowStates)-1]
		resumable := latest.Resumable
		scenario.RequireWorkflowState = &WorkflowStateExpectation{
			Action:              latest.Action,
			Kind:                latest.Kind,
			Phase:               latest.Phase,
			SourceLane:          latest.SourceLane,
			RequireTargetRefs:   append([]string(nil), latest.TargetRefs...),
			RequireEditable:     append([]string(nil), latest.EditableTargets...),
			RequireEvidence:     append([]string(nil), latest.EvidencePaths...),
			RequireReadOnly:     append([]string(nil), latest.ReadOnlyEvidence...),
			RequireDestinations: append([]string(nil), latest.AllowedDestinations...),
			RequireVerification: append([]string(nil), latest.Verification...),
			RequireNextActions:  append([]string(nil), latest.NextActions...),
			Resumable:           &resumable,
		}
	}
	if len(metrics.Replans) > 0 {
		latest := metrics.Replans[len(metrics.Replans)-1]
		scenario.RequireReplan = &ReplanExpectation{
			FromLane: latest.FromLane,
			ToLane:   latest.ToLane,
			Reason:   latest.Reason,
		}
	}
	if metrics.ApprovalRequired {
		yes := true
		scenario.RequireApproval = &yes
	}
	if metrics.BroadToolRequests > 0 {
		max := metrics.BroadToolRequests
		scenario.MaxBroadToolRequests = &max
	}
	if metrics.HeavyDiscoveryRequests > 0 {
		max := metrics.HeavyDiscoveryRequests
		scenario.MaxHeavyDiscoveryRequests = &max
	}
	deadEnds := metrics.RepeatedDeadEndRejections + metrics.RepeatedToolRejections
	if deadEnds > 0 {
		scenario.MaxRepeatedDeadEnds = &deadEnds
	}
	if metrics.SummaryRejections > 0 {
		max := metrics.SummaryRejections
		scenario.MaxSummaryRejections = &max
	}
	return scenario
}

func nonEmptySingleton(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return []string{value}
}

func suggestedTools(records []Record) []string {
	type requestInfo struct {
		Tool  string
		Broad bool
	}
	requests := make(map[int]requestInfo)
	tools := make([]string, 0, 4)
	seen := make(map[string]struct{})
	for _, record := range records {
		switch record.Type {
		case "tool_request":
			step := intField(record.Data, "step")
			if step <= 0 {
				continue
			}
			requests[step] = requestInfo{
				Tool:  stringField(record.Data, "tool"),
				Broad: boolField(record.Data, "broad"),
			}
		case "tool_result":
			step := intField(record.Data, "step")
			req, ok := requests[step]
			if !ok || req.Broad || strings.TrimSpace(stringField(record.Data, "error")) != "" {
				continue
			}
			tool := strings.TrimSpace(req.Tool)
			if !isSuggestedScenarioTool(tool) {
				continue
			}
			if _, ok := seen[tool]; ok {
				continue
			}
			seen[tool] = struct{}{}
			tools = append(tools, tool)
		}
	}
	slices.Sort(tools)
	return tools
}

func isSuggestedScenarioTool(tool string) bool {
	switch strings.TrimSpace(tool) {
	case "read_file", "search_content", "code_navigate", "source_sanity", "edit_file", "write_file", "append_file", "mkdir", "shell",
		"discovery_query", "structural_search", "dependency_graph", "history_range", "history_symbol", "blame_range", "ctags_symbols", "semgrep_scan", "joern_scan",
		"go_overview", "go_entrypoints", "go_find_symbol", "go_read_symbol", "go_definition", "go_references",
		"go_implementations", "go_calls_from", "go_calls_into", "go_extract_body", "go_importers",
		"go_diagnostics", "go_workspace_symbol", "go_related_files", "go_find_package", "go_find_type", "go_find_const":
		return true
	default:
		return false
	}
}

func compactLanguageCapabilityExpectations(caps []LanguageCapabilitySnapshot) []LanguageCapabilityExpectation {
	caps = mergeLanguageCapabilities(nil, caps)
	out := make([]LanguageCapabilityExpectation, 0, len(caps))
	for _, cap := range caps {
		expectation := LanguageCapabilityExpectation{Language: cap.Language}
		if len(cap.Tools) > 0 {
			expectation.Tool = cap.Tools[0]
		}
		if len(cap.Signals) > 0 {
			expectation.Signal = cap.Signals[0]
		}
		out = append(out, expectation)
	}
	return out
}

func compactPhaseExpectations(phases []PhaseSnapshot) []PhaseExpectation {
	seen := make(map[string]struct{})
	out := make([]PhaseExpectation, 0, len(phases))
	for _, phase := range phases {
		key := strings.Join([]string{phase.Lane, phase.Phase, phase.State}, "|")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, PhaseExpectation{Lane: phase.Lane, Phase: phase.Phase, State: phase.State})
	}
	return out
}
