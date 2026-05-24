package agent

import (
	"fmt"
	"slices"
	"strings"

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
	if contract.PhaseID == "" || contract.PhaseID == turnPhaseGeneric {
		return state
	}
	if strings.TrimSpace(contract.Phase) != "" {
		state.Phase = contract.Phase
	}
	if len(contract.RequiredNext) > 0 {
		state.NextValidActions = append([]string(nil), contract.RequiredNext...)
	}
	if len(contract.InvalidNext) > 0 {
		state.InvalidNextActions = append([]string(nil), contract.InvalidNext...)
	}
	state.Constraints = mergeRunnerStateStrings(state.Constraints, contract.Constraints)
	state.StateSummary = runnerStateSummary(state)
	return state
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
	if planThenExecuteRejectedPlanNeedsSourceRefresh(brief, results) {
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
