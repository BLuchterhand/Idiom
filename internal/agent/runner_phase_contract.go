package agent

import (
	"fmt"
	"slices"
	"strings"

	"idiom/internal/autonomy"
	"idiom/internal/frontier/workerruntime"
	"idiom/internal/llama"
	"idiom/internal/workspace"
)

func (r *Runner) deriveCurrentTurnPhaseContract(input runnerTurnPhaseContractInput) TurnPhaseContract {
	brief := workerruntime.NormalizeRuntimeBrief(input.Brief)
	return deriveTurnPhaseContract(TurnPhaseContractInput{
		ContractID:               strings.TrimSpace(input.ContractID),
		PhaseEpoch:               input.ModelCallNumber,
		ModelCallID:              runnerTurnPhaseModelCallID(input.ContractID, input.ModelCallNumber),
		Step:                     input.Step,
		PhaseHint:                input.PhaseHint,
		Brief:                    brief,
		Results:                  input.Results,
		BaseAllowedTools:         append([]string(nil), input.AllowedTools...),
		HiddenTools:              runnerTurnPhaseHiddenTools(input.HiddenTools),
		BackendKind:              runnerTurnPhaseBackendKind(r),
		ModelAlias:               input.Options.Model,
		ToolTransportMode:        runnerTurnPhaseTransportMode(input.Options, input.SupportsNativeTools),
		ParserContract:           runnerTurnPhaseParserContract(input.Options, brief, input.Results, input.SupportsNativeTools),
		ResponseFormat:           runnerTurnPhaseResponseFormat(input.Options),
		Temperature:              input.Options.Temperature,
		TopP:                     input.Options.TopP,
		TopK:                     input.Options.TopK,
		MinP:                     input.Options.MinP,
		PresencePenalty:          input.Options.PresencePenalty,
		RepetitionPenalty:        input.Options.RepetitionPenalty,
		ToolChoice:               input.Options.ToolChoice,
		ThinkingPolicy:           runnerTurnPhaseThinkingPolicy(input.Options),
		MaxTokens:                input.Options.MaxTokens,
		SupportsNativeTools:      input.SupportsNativeTools,
		TextualToolParser:        runnerTurnPhaseTextualParser(input.Options, input.SupportsNativeTools),
		AcceptanceRefs:           append([]string(nil), brief.RequiredEvidence...),
		VerificationEvidenceRefs: runnerVerificationEvidenceRefs(input.Results),
		VerificationStatus:       runnerAutonomyVerificationStatus(input.Results, brief),
		ResidualBlockers:         runnerTurnPhaseResidualBlockers(input.Results),
	})
}

func applyTurnPhaseContractToNextActionState(state runnerNextActionState, contract TurnPhaseContract) runnerNextActionState {
	if contract.PhaseID == "" {
		return state
	}
	if contract.PhaseID == turnPhaseGeneric && strings.TrimSpace(contract.Phase) == "generic" {
		return state
	}
	if strings.TrimSpace(contract.Phase) != "" {
		state.Phase = contract.Phase
	}
	if len(contract.RequiredNext) > 0 {
		state.NextValidActions = append([]string(nil), contract.RequiredNext...)
	} else if turnPhaseContractTerminalOutcomesDriveNextActions(contract) {
		state.NextValidActions = append([]string(nil), contract.TerminalOutcomes...)
		if containsPhaseString(contract.TerminalOutcomes, actionSubmitPlan) {
			state.NextValidActions = []string{"submit_plan covering every required_edit_path with real edits or source_satisfied_paths entries, Decision: source-satisfied with per-path current-state evidence, or <<QUESTION>> with the exact blocker"}
		}
	}
	if len(contract.InvalidNext) > 0 {
		state.InvalidNextActions = append([]string(nil), contract.InvalidNext...)
	}
	state.Constraints = mergeRunnerStateStrings(state.Constraints, contract.Constraints)
	state.StateSummary = runnerStateSummary(state)
	return state
}

func turnPhaseSurfaceSummaryFromNextActionState(state runnerNextActionState, contract TurnPhaseContract) autonomy.TurnPhaseSurfaceSummaryEvent {
	payload := contract.autonomyEventPayload()
	advertisedClasses := turnPhaseSurfaceActionClassesFromText(state.NextValidActions)
	invalidClasses := turnPhaseSurfaceInvalidActionClassesFromText(state.InvalidNextActions)
	invalidClasses = removeAllowedTurnPhaseInvalidActionClasses(invalidClasses, contract.ActionPolicy.AllowedActionClasses)
	return autonomy.TurnPhaseSurfaceSummaryEvent{
		SchemaVersion:           autonomy.TurnPhaseSurfaceSummarySchemaVersion,
		SurfaceID:               "next_action_state",
		ContractID:              contract.ContractID,
		PhaseID:                 contract.PhaseID,
		ModelCallID:             contract.ModelCallID,
		ProtocolHash:            payload.ProtocolHash,
		AdvertisedActionClasses: advertisedClasses,
		InvalidActionClasses:    invalidClasses,
		ObligationRefs:          append([]string(nil), state.EvidencePaths...),
	}
}

func turnPhaseSurfaceActionClassesFromText(values []string) []string {
	var classes []string
	for _, value := range values {
		lower := strings.ToLower(strings.TrimSpace(value))
		if lower == "" {
			continue
		}
		if strings.Contains(lower, "submit_plan") {
			classes = append(classes, actionSubmitPlan)
		}
		if strings.Contains(lower, "source-satisfied") {
			classes = append(classes, actionSourceSatisfied)
		}
		if strings.Contains(lower, "<<question>>") || strings.Contains(lower, "blocker") {
			classes = append(classes, actionBlocker)
		}
		if strings.Contains(lower, "decision: pass") {
			classes = append(classes, actionReviewPass)
		}
		if strings.Contains(lower, "final answer") || strings.Contains(lower, "answer directly") || strings.Contains(lower, "answer from") {
			classes = append(classes, actionFinalAnswer)
		}
		if phaseActionTextHasTerm(lower, "read") || phaseActionTextHasTerm(lower, "inspect") {
			classes = append(classes, actionRead)
		}
		if phaseActionTextHasTerm(lower, "search") || phaseActionTextHasTerm(lower, "discovery") || phaseActionTextHasTerm(lower, "context") {
			classes = append(classes, actionSearch)
		}
		if !strings.Contains(lower, "submit_plan") && (strings.Contains(lower, "write") || strings.Contains(lower, "edit") || strings.Contains(lower, "patch")) {
			classes = append(classes, actionPerEditWrite)
		}
		if phaseActionTextHasTerm(lower, "verify") || phaseActionTextHasTerm(lower, "test") || phaseActionTextHasTerm(lower, "command") {
			classes = append(classes, actionVerify)
		}
	}
	return uniquePhaseStrings(classes)
}

func phaseActionTextHasTerm(value string, term string) bool {
	if value == "" || term == "" {
		return false
	}
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_')
	})
	for _, field := range fields {
		if field == term {
			return true
		}
	}
	return false
}

func turnPhaseContractTerminalOutcomesDriveNextActions(contract TurnPhaseContract) bool {
	if len(contract.TerminalOutcomes) == 0 {
		return false
	}
	phaseID := strings.TrimSpace(contract.PhaseID)
	return strings.Contains(phaseID, "submit_ready") ||
		strings.Contains(phaseID, "review_repair") ||
		strings.Contains(phaseID, "final")
}

func turnPhaseSurfaceInvalidActionClassesFromText(values []string) []string {
	var classes []string
	for _, value := range values {
		classes = append(classes, turnPhaseSurfaceActionClassesFromText([]string{turnPhaseSurfaceInvalidActionLeadClause(value)})...)
	}
	out := classes[:0]
	for _, actionClass := range classes {
		switch actionClass {
		case actionSubmitPlan, actionSourceSatisfied, actionBlocker, actionReviewPass, actionFinalAnswer:
			continue
		default:
			out = append(out, actionClass)
		}
	}
	return out
}

func removeAllowedTurnPhaseInvalidActionClasses(invalidClasses []string, allowedClasses []string) []string {
	if len(invalidClasses) == 0 || len(allowedClasses) == 0 {
		return invalidClasses
	}
	out := invalidClasses[:0]
	for _, actionClass := range invalidClasses {
		if containsPhaseString(allowedClasses, actionClass) {
			continue
		}
		out = append(out, actionClass)
	}
	return out
}

func turnPhaseSurfaceInvalidActionLeadClause(value string) string {
	lower := strings.ToLower(strings.TrimSpace(value))
	if lower == "" {
		return ""
	}
	for _, marker := range []string{
		" before ",
		" after ",
		" while ",
		" until ",
		" unless ",
		" except ",
		" without ",
	} {
		if before, _, ok := strings.Cut(lower, marker); ok {
			return before
		}
	}
	return lower
}

type runnerTurnPhaseContractInput struct {
	ContractID          string
	ModelCallNumber     int
	Step                int
	PhaseHint           string
	Brief               workerruntime.WorkerRuntimeBrief
	Results             []workspace.ToolResult
	AllowedTools        []string
	HiddenTools         map[string]bool
	Options             llama.ChatOptions
	SupportsNativeTools bool
}

func runnerTurnPhaseModelCallID(contractID string, modelCallNumber int) string {
	contractID = strings.TrimSpace(contractID)
	if contractID == "" {
		return fmt.Sprintf("model_call_%d", modelCallNumber)
	}
	return fmt.Sprintf("%s:model_call_%d", contractID, modelCallNumber)
}

func runnerTurnPhaseHiddenTools(hidden map[string]bool) []string {
	if len(hidden) == 0 {
		return nil
	}
	out := make([]string, 0, len(hidden))
	for tool, isHidden := range hidden {
		if !isHidden {
			continue
		}
		if trimmed := strings.TrimSpace(tool); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	slices.Sort(out)
	return out
}

func runnerTurnPhaseBackendKind(r *Runner) string {
	if r == nil || r.Client == nil {
		return ""
	}
	return r.Client.Backend()
}

func runnerTurnPhaseTransportMode(opts llama.ChatOptions, supportsNativeTools bool) string {
	return PlanExecuteRuntimeCallPolicyFromChatOptions(opts, supportsNativeTools, false).PhaseTransportMode()
}

func runnerTurnPhaseParserContract(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, supportsNativeTools bool) string {
	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode == PlanExecutePlanningModeAnchorRefresh {
		return "source_refresh"
	}
	if opts.ResponseFormatJSON {
		return "submit_plan_json"
	}
	if len(opts.Tools) == 1 && strings.TrimSpace(opts.Tools[0].Function.Name) == "submit_plan" && supportsNativeTools {
		return "native_submit_plan"
	}
	return ""
}

func runnerTurnPhaseResponseFormat(opts llama.ChatOptions) string {
	if opts.ResponseFormatJSON {
		return "json"
	}
	return ""
}

func runnerTurnPhaseThinkingPolicy(opts llama.ChatOptions) string {
	policy := thinkingOptionForLog(opts.EnableThinking)
	if opts.PreserveThinking {
		return policy + "_preserve"
	}
	return policy + "_visible"
}

func runnerTurnPhaseTextualParser(opts llama.ChatOptions, supportsNativeTools bool) bool {
	return PlanExecuteRuntimeCallPolicyFromChatOptions(opts, supportsNativeTools, false).TextualToolParser
}

func runnerVerificationEvidenceRefs(results []workspace.ToolResult) []string {
	var refs []string
	for _, result := range results {
		if !resultCountsAsSuccessfulVerification(result) {
			continue
		}
		summary := strings.TrimSpace(result.Summary)
		if summary == "" {
			summary = strings.TrimSpace(result.Tool)
		}
		if summary != "" {
			refs = append(refs, summary)
		}
	}
	return uniquePhaseStrings(refs)
}

func runnerTurnPhaseResidualBlockers(results []workspace.ToolResult) []string {
	var blockers []string
	for _, result := range results {
		if strings.TrimSpace(result.Error) == "" {
			continue
		}
		if strings.TrimSpace(result.Tool) == "completion_gate" || strings.Contains(strings.ToLower(result.Summary), "blocked") {
			blockers = append(blockers, result.Summary)
		}
	}
	return uniquePhaseStrings(blockers)
}
