package agent

import "strings"

type RuntimeCallTransportMode string

const (
	RuntimeCallTransportModeTextual     RuntimeCallTransportMode = "textual_tools"
	RuntimeCallTransportModeTextualJSON RuntimeCallTransportMode = "textual_json_terminal"
	RuntimeCallTransportModeNative      RuntimeCallTransportMode = "native_tools"
)

type RuntimeCallPolicyInput struct {
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

type RuntimeCallPolicy struct {
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
	TransportMode            RuntimeCallTransportMode
	ExpressibleActionClasses []string
}

func DeriveRuntimeCallPolicy(input RuntimeCallPolicyInput) RuntimeCallPolicy {
	responseFormatJSON := input.ResponseFormatJSON || strings.EqualFold(strings.TrimSpace(input.ResponseFormat), "json")
	textualParser := input.TextualToolParser || responseFormatJSON || (!input.SupportsNativeTools && !input.ForceNativeTools)
	policy := RuntimeCallPolicy{
		SupportsNativeTools:      input.SupportsNativeTools,
		ForceNativeTools:         input.ForceNativeTools,
		TextualToolParser:        textualParser,
		TerminalOnly:             input.TerminalOnly,
		ResponseFormatJSON:       responseFormatJSON,
		Temperature:              input.Temperature,
		TopP:                     input.TopP,
		TopK:                     input.TopK,
		MinP:                     input.MinP,
		PresencePenalty:          input.PresencePenalty,
		RepetitionPenalty:        input.RepetitionPenalty,
		ToolChoice:               strings.TrimSpace(input.ToolChoice),
		ToolChoiceRequired:       strings.EqualFold(strings.TrimSpace(input.ToolChoice), "required"),
		ParserContract:           strings.TrimSpace(input.ParserContract),
		ResponseFormat:           strings.TrimSpace(input.ResponseFormat),
		ThinkingPolicy:           strings.TrimSpace(input.ThinkingPolicy),
		EnableThinking:           cloneBoolPtr(input.EnableThinking),
		PreserveThinking:         input.PreserveThinking,
		ThinkingBudget:           input.ThinkingBudget,
		ChatTemplateKwargs:       input.ChatTemplateKwargs,
		MaxTokens:                input.MaxTokens,
		TimeoutMillis:            input.TimeoutMillis,
		ExpressibleActionClasses: uniquePhaseStrings(input.ExpressibleActionClasses),
	}
	policy.TransportMode = runtimeCallTransportMode(policy)
	return policy
}

func (policy RuntimeCallPolicy) ToolTransportMode() string {
	switch policy.TransportMode {
	case RuntimeCallTransportModeNative:
		return "native"
	case RuntimeCallTransportModeTextual, RuntimeCallTransportModeTextualJSON:
		return "textual"
	default:
		return ""
	}
}

func (policy RuntimeCallPolicy) ResponseFormatMode() string {
	if policy.ResponseFormatJSON {
		return "json"
	}
	if responseFormat := strings.TrimSpace(policy.ResponseFormat); responseFormat != "" {
		return responseFormat
	}
	return "default"
}

func runtimeCallTransportMode(policy RuntimeCallPolicy) RuntimeCallTransportMode {
	if policy.ForceNativeTools || (policy.SupportsNativeTools && !policy.TextualToolParser && !policy.ResponseFormatJSON) {
		return RuntimeCallTransportModeNative
	}
	if policy.TerminalOnly || policy.ResponseFormatJSON {
		return RuntimeCallTransportModeTextualJSON
	}
	return RuntimeCallTransportModeTextual
}
