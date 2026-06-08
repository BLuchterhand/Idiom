package agent

import (
	"strings"

	"idiom/internal/platform/util"
	"idiom/internal/runtime/llama"
)

type PlanExecuteRuntimeCallPolicyInput struct {
	SupportsNativeTools      bool
	ForceNativeTools         bool
	TextualToolParser        bool
	TerminalOnly             bool
	ResponseFormatJSON       bool
	Temperature              float64
	TopP                     float64
	TopK                     int
	MinP                     float64
	PresencePenalty          float64
	RepetitionPenalty        float64
	ToolChoice               string
	ParserContract           string
	ResponseFormat           string
	ThinkingPolicy           string
	EnableThinking           *bool
	PreserveThinking         bool
	ThinkingBudget           int
	ChatTemplateKwargs       bool
	MaxTokens                int
	TimeoutMillis            int
	ExpressibleActionClasses []string
}

type PlanExecuteRuntimeCallPolicy struct {
	SupportsNativeTools      bool
	ForceNativeTools         bool
	TextualToolParser        bool
	TerminalOnly             bool
	ResponseFormatJSON       bool
	Temperature              float64
	TopP                     float64
	TopK                     int
	MinP                     float64
	PresencePenalty          float64
	RepetitionPenalty        float64
	ToolChoice               string
	ToolChoiceRequired       bool
	ParserContract           string
	ResponseFormat           string
	ThinkingPolicy           string
	EnableThinking           *bool
	PreserveThinking         bool
	ThinkingBudget           int
	ChatTemplateKwargs       bool
	MaxTokens                int
	TimeoutMillis            int
	TransportMode            PlanExecuteTransportMode
	ExpressibleActionClasses []string
}

func DerivePlanExecuteRuntimeCallPolicy(input PlanExecuteRuntimeCallPolicyInput) PlanExecuteRuntimeCallPolicy {
	terminalOnly := input.TerminalOnly || util.EqualTrimFold(input.ParserContract, "submit_plan_json")
	runtimePolicy := DeriveRuntimeCallPolicy(RuntimeCallPolicyInput{
		SupportsNativeTools:      input.SupportsNativeTools,
		ForceNativeTools:         input.ForceNativeTools,
		TextualToolParser:        input.TextualToolParser,
		TerminalOnly:             terminalOnly,
		ResponseFormatJSON:       input.ResponseFormatJSON,
		Temperature:              input.Temperature,
		TopP:                     input.TopP,
		TopK:                     input.TopK,
		MinP:                     input.MinP,
		PresencePenalty:          input.PresencePenalty,
		RepetitionPenalty:        input.RepetitionPenalty,
		ToolChoice:               input.ToolChoice,
		ParserContract:           input.ParserContract,
		ResponseFormat:           input.ResponseFormat,
		ThinkingPolicy:           input.ThinkingPolicy,
		EnableThinking:           input.EnableThinking,
		PreserveThinking:         input.PreserveThinking,
		ThinkingBudget:           input.ThinkingBudget,
		ChatTemplateKwargs:       input.ChatTemplateKwargs,
		MaxTokens:                input.MaxTokens,
		TimeoutMillis:            input.TimeoutMillis,
		ExpressibleActionClasses: input.ExpressibleActionClasses,
	})
	policy := PlanExecuteRuntimeCallPolicy{
		SupportsNativeTools:      runtimePolicy.SupportsNativeTools,
		ForceNativeTools:         runtimePolicy.ForceNativeTools,
		TextualToolParser:        runtimePolicy.TextualToolParser,
		TerminalOnly:             runtimePolicy.TerminalOnly,
		ResponseFormatJSON:       runtimePolicy.ResponseFormatJSON,
		Temperature:              runtimePolicy.Temperature,
		TopP:                     runtimePolicy.TopP,
		TopK:                     runtimePolicy.TopK,
		MinP:                     runtimePolicy.MinP,
		PresencePenalty:          runtimePolicy.PresencePenalty,
		RepetitionPenalty:        runtimePolicy.RepetitionPenalty,
		ToolChoice:               runtimePolicy.ToolChoice,
		ToolChoiceRequired:       runtimePolicy.ToolChoiceRequired,
		ParserContract:           runtimePolicy.ParserContract,
		ResponseFormat:           runtimePolicy.ResponseFormat,
		ThinkingPolicy:           runtimePolicy.ThinkingPolicy,
		EnableThinking:           cloneBoolPtr(runtimePolicy.EnableThinking),
		PreserveThinking:         runtimePolicy.PreserveThinking,
		ThinkingBudget:           runtimePolicy.ThinkingBudget,
		ChatTemplateKwargs:       runtimePolicy.ChatTemplateKwargs,
		MaxTokens:                runtimePolicy.MaxTokens,
		TimeoutMillis:            runtimePolicy.TimeoutMillis,
		TransportMode:            planExecuteTransportModeFromRuntime(runtimePolicy.TransportMode),
		ExpressibleActionClasses: append([]string(nil), runtimePolicy.ExpressibleActionClasses...),
	}
	return policy
}

func PlanExecuteRuntimeCallPolicyFromChatOptions(opts llama.ChatOptions, supportsNativeTools bool, terminalOnly bool) PlanExecuteRuntimeCallPolicy {
	return DerivePlanExecuteRuntimeCallPolicy(PlanExecuteRuntimeCallPolicyInput{
		SupportsNativeTools: supportsNativeTools,
		ForceNativeTools:    opts.ForceNativeTools,
		TextualToolParser:   planExecuteTextualToolParserFromChatOptions(opts, supportsNativeTools),
		TerminalOnly:        terminalOnly,
		ResponseFormatJSON:  opts.ResponseFormatJSON,
		Temperature:         opts.Temperature,
		TopP:                opts.TopP,
		TopK:                opts.TopK,
		MinP:                opts.MinP,
		PresencePenalty:     opts.PresencePenalty,
		RepetitionPenalty:   opts.RepetitionPenalty,
		ToolChoice:          opts.ToolChoice,
		ThinkingPolicy:      runnerTurnPhaseThinkingPolicy(opts),
		EnableThinking:      opts.EnableThinking,
		PreserveThinking:    opts.PreserveThinking,
		ThinkingBudget:      opts.ThinkingBudget,
		ChatTemplateKwargs:  opts.ChatTemplateKwargs,
		MaxTokens:           opts.MaxTokens,
	})
}

func planExecuteRuntimeCallPolicyFromPhaseInput(input TurnPhaseContractInput) PlanExecuteRuntimeCallPolicy {
	return DerivePlanExecuteRuntimeCallPolicy(PlanExecuteRuntimeCallPolicyInput{
		SupportsNativeTools: input.SupportsNativeTools,
		TextualToolParser:   input.TextualToolParser,
		TerminalOnly:        false,
		ResponseFormatJSON:  util.EqualTrimFold(input.ResponseFormat, "json"),
		Temperature:         input.Temperature,
		TopP:                input.TopP,
		TopK:                input.TopK,
		MinP:                input.MinP,
		PresencePenalty:     input.PresencePenalty,
		RepetitionPenalty:   input.RepetitionPenalty,
		ToolChoice:          input.ToolChoice,
		ParserContract:      input.ParserContract,
		ResponseFormat:      input.ResponseFormat,
		ThinkingPolicy:      input.ThinkingPolicy,
		MaxTokens:           input.MaxTokens,
	})
}

func (policy PlanExecuteRuntimeCallPolicy) PlanningTransport() PlanExecutePlanningTransport {
	return PlanExecutePlanningTransport{
		SupportsNativeTools: policy.SupportsNativeTools,
		ForceNativeTools:    policy.ForceNativeTools,
		TextualToolParser:   policy.TextualToolParser,
		TerminalOnly:        policy.TerminalOnly,
		ResponseFormatJSON:  policy.ResponseFormatJSON,
		EffectiveMaxTokens:  policy.MaxTokens,
		TimeoutMillis:       policy.TimeoutMillis,
	}
}

func (policy PlanExecuteRuntimeCallPolicy) RuntimeCallPolicy() RuntimeCallPolicy {
	return RuntimeCallPolicy{
		SupportsNativeTools:      policy.SupportsNativeTools,
		ForceNativeTools:         policy.ForceNativeTools,
		TextualToolParser:        policy.TextualToolParser,
		TerminalOnly:             policy.TerminalOnly,
		ResponseFormatJSON:       policy.ResponseFormatJSON,
		Temperature:              policy.Temperature,
		TopP:                     policy.TopP,
		TopK:                     policy.TopK,
		MinP:                     policy.MinP,
		PresencePenalty:          policy.PresencePenalty,
		RepetitionPenalty:        policy.RepetitionPenalty,
		ToolChoice:               strings.TrimSpace(policy.ToolChoice),
		ToolChoiceRequired:       policy.ToolChoiceRequired,
		ParserContract:           strings.TrimSpace(policy.ParserContract),
		ResponseFormat:           strings.TrimSpace(policy.ResponseFormat),
		ThinkingPolicy:           strings.TrimSpace(policy.ThinkingPolicy),
		EnableThinking:           cloneBoolPtr(policy.EnableThinking),
		PreserveThinking:         policy.PreserveThinking,
		ThinkingBudget:           policy.ThinkingBudget,
		ChatTemplateKwargs:       policy.ChatTemplateKwargs,
		MaxTokens:                policy.MaxTokens,
		TimeoutMillis:            policy.TimeoutMillis,
		TransportMode:            runtimeCallTransportModeFromPlanExecute(policy.TransportMode),
		ExpressibleActionClasses: append([]string(nil), policy.ExpressibleActionClasses...),
	}
}

func (policy PlanExecuteRuntimeCallPolicy) PhaseTransportMode() string {
	return policy.RuntimeCallPolicy().ToolTransportMode()
}

func (policy PlanExecuteRuntimeCallPolicy) ResponseFormatMode() string {
	return policy.RuntimeCallPolicy().ResponseFormatMode()
}

func (policy PlanExecuteRuntimeCallPolicy) TelemetryFields() map[string]any {
	fields := map[string]any{
		"transport_mode":         policy.PhaseTransportMode(),
		"runtime_transport_mode": string(policy.TransportMode),
		"parser_contract":        strings.TrimSpace(policy.ParserContract),
		"response_format":        runnerTelemetryResponseFormat(policy),
		"response_format_mode":   policy.ResponseFormatMode(),
		"response_format_json":   policy.ResponseFormatJSON,
		"temperature":            policy.Temperature,
		"top_p":                  policy.TopP,
		"top_k":                  policy.TopK,
		"min_p":                  policy.MinP,
		"presence_penalty":       policy.PresencePenalty,
		"repetition_penalty":     policy.RepetitionPenalty,
		"max_tokens":             policy.MaxTokens,
		"effective_max_tokens":   policy.MaxTokens,
		"thinking_policy":        strings.TrimSpace(policy.ThinkingPolicy),
		"thinking_preserved":     policy.PreserveThinking,
		"thinking_budget":        policy.ThinkingBudget,
		"chat_template_kwargs":   policy.ChatTemplateKwargs,
		"tool_choice":            strings.TrimSpace(policy.ToolChoice),
		"tool_choice_required":   policy.ToolChoiceRequired,
		"force_native_tools":     policy.ForceNativeTools,
		"supports_native_tools":  policy.SupportsNativeTools,
		"textual_tool_parser":    policy.TextualToolParser,
	}
	if policy.EnableThinking != nil {
		fields["thinking_enabled"] = *policy.EnableThinking
	}
	if policy.TimeoutMillis > 0 {
		fields["effective_timeout_ms"] = policy.TimeoutMillis
	}
	return fields
}

func runnerTelemetryResponseFormat(policy PlanExecuteRuntimeCallPolicy) string {
	if policy.ResponseFormatJSON {
		return "json"
	}
	return strings.TrimSpace(policy.ResponseFormat)
}

func planExecuteTransportModeFromRuntime(mode RuntimeCallTransportMode) PlanExecuteTransportMode {
	switch mode {
	case RuntimeCallTransportModeNative:
		return PlanExecuteTransportModeNative
	case RuntimeCallTransportModeTextualJSON:
		return PlanExecuteTransportModeTextualJSON
	default:
		return PlanExecuteTransportModeTextual
	}
}

func runtimeCallTransportModeFromPlanExecute(mode PlanExecuteTransportMode) RuntimeCallTransportMode {
	switch mode {
	case PlanExecuteTransportModeNative:
		return RuntimeCallTransportModeNative
	case PlanExecuteTransportModeTextualJSON:
		return RuntimeCallTransportModeTextualJSON
	default:
		return RuntimeCallTransportModeTextual
	}
}

func planExecuteTextualToolParserFromChatOptions(opts llama.ChatOptions, supportsNativeTools bool) bool {
	return opts.ResponseFormatJSON || len(opts.Tools) == 0 || (!supportsNativeTools && !opts.ForceNativeTools)
}

func planExecuteForceVisibleActionChatOptions(opts llama.ChatOptions) llama.ChatOptions {
	thinkingOff := false
	opts.EnableThinking = &thinkingOff
	opts.ChatTemplateKwargs = true
	opts.PreserveThinking = false
	return opts
}

func planExecuteSubmitPlanTerminalChatOptions(opts llama.ChatOptions, supportsNativeTools bool) llama.ChatOptions {
	opts = planExecuteForceVisibleActionChatOptions(opts)
	if !supportsNativeTools {
		opts.Tools = nil
		opts.ToolChoice = ""
		opts.ResponseFormatJSON = true
	} else {
		opts.ResponseFormatJSON = false
	}
	return opts
}

func planExecuteSetExactMaxTokens(opts llama.ChatOptions, target int) llama.ChatOptions {
	if target <= 0 {
		return opts
	}
	if opts.MaxTokens <= 0 || opts.MaxTokens < target || opts.MaxTokens > target {
		opts.MaxTokens = target
	}
	return opts
}

func planExecuteCapMaxTokens(opts llama.ChatOptions, limit int) llama.ChatOptions {
	if limit <= 0 {
		return opts
	}
	if opts.MaxTokens <= 0 || opts.MaxTokens > limit {
		opts.MaxTokens = limit
	}
	return opts
}
