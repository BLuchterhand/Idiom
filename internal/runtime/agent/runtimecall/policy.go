package runtimecall

import (
	"strings"

	"idiom/internal/runtime/agent/stringset"
)

type TransportMode string

const (
	TransportModeTextual     TransportMode = "textual_tools"
	TransportModeTextualJSON TransportMode = "textual_json_terminal"
	TransportModeNative      TransportMode = "native_tools"
)

type PolicyInput struct {
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

type Policy struct {
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
	TransportMode            TransportMode
	ExpressibleActionClasses []string
}

func Derive(input PolicyInput) Policy {
	responseFormatJSON := input.ResponseFormatJSON || strings.EqualFold(strings.TrimSpace(input.ResponseFormat), "json")
	textualParser := input.TextualToolParser || responseFormatJSON || (!input.SupportsNativeTools && !input.ForceNativeTools)
	policy := Policy{
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
		ExpressibleActionClasses: UniqueStrings(input.ExpressibleActionClasses),
	}
	policy.TransportMode = transportMode(policy)
	return policy
}

func (policy Policy) ToolTransportMode() string {
	switch policy.TransportMode {
	case TransportModeNative:
		return "native"
	case TransportModeTextual, TransportModeTextualJSON:
		return "textual"
	default:
		return ""
	}
}

func (policy Policy) ResponseFormatMode() string {
	if policy.ResponseFormatJSON {
		return "json"
	}
	if responseFormat := strings.TrimSpace(policy.ResponseFormat); responseFormat != "" {
		return responseFormat
	}
	return "default"
}

func transportMode(policy Policy) TransportMode {
	if policy.ForceNativeTools || (policy.SupportsNativeTools && !policy.TextualToolParser && !policy.ResponseFormatJSON) {
		return TransportModeNative
	}
	if policy.TerminalOnly || policy.ResponseFormatJSON {
		return TransportModeTextualJSON
	}
	return TransportModeTextual
}

func CloneBoolPtr(value *bool) *bool {
	return cloneBoolPtr(value)
}

func cloneBoolPtr(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func UniqueStrings(values []string) []string {
	return stringset.Unique(values)
}
