package agent

import "testing"

func TestDeriveRuntimeCallPolicyNormalizesTransportAndContractFields(t *testing.T) {
	thinkingEnabled := false

	policy := DeriveRuntimeCallPolicy(RuntimeCallPolicyInput{
		SupportsNativeTools:      true,
		ResponseFormat:           " json ",
		Temperature:              0.2,
		TopP:                     1,
		TopK:                     20,
		MinP:                     0.05,
		PresencePenalty:          0.1,
		RepetitionPenalty:        1.1,
		ToolChoice:               " required ",
		ParserContract:           " submit_plan_json ",
		ThinkingPolicy:           " disabled_visible ",
		EnableThinking:           &thinkingEnabled,
		PreserveThinking:         false,
		ThinkingBudget:           128,
		ChatTemplateKwargs:       true,
		MaxTokens:                512,
		TimeoutMillis:            2500,
		ExpressibleActionClasses: []string{actionSubmitPlan, actionBlocker, actionSubmitPlan},
	})

	if policy.TransportMode != RuntimeCallTransportModeTextualJSON {
		t.Fatalf("TransportMode = %q, want %q", policy.TransportMode, RuntimeCallTransportModeTextualJSON)
	}
	if policy.ToolTransportMode() != "textual" {
		t.Fatalf("ToolTransportMode = %q, want textual", policy.ToolTransportMode())
	}
	if !policy.ResponseFormatJSON || policy.ResponseFormatMode() != "json" {
		t.Fatalf("response format = json:%v mode:%q, want json", policy.ResponseFormatJSON, policy.ResponseFormatMode())
	}
	if !policy.ToolChoiceRequired || policy.ToolChoice != "required" {
		t.Fatalf("tool choice = required:%v value:%q, want required", policy.ToolChoiceRequired, policy.ToolChoice)
	}
	if policy.ParserContract != "submit_plan_json" {
		t.Fatalf("ParserContract = %q, want submit_plan_json", policy.ParserContract)
	}
	if policy.Temperature != 0.2 || policy.TopP != 1 || policy.TopK != 20 || policy.MinP != 0.05 || policy.PresencePenalty != 0.1 || policy.RepetitionPenalty != 1.1 {
		t.Fatalf("sampling fields not preserved: %#v", policy)
	}
	assertEqualSet(t, policy.ExpressibleActionClasses, []string{actionSubmitPlan, actionBlocker})
	if policy.EnableThinking == nil || *policy.EnableThinking {
		t.Fatalf("EnableThinking = %#v, want false pointer", policy.EnableThinking)
	}
}

func TestDeriveRuntimeCallPolicyUsesNativeToolsWhenParserIsNative(t *testing.T) {
	policy := DeriveRuntimeCallPolicy(RuntimeCallPolicyInput{
		SupportsNativeTools: true,
		ToolChoice:          "required",
		MaxTokens:           4096,
	})

	if policy.TransportMode != RuntimeCallTransportModeNative {
		t.Fatalf("TransportMode = %q, want %q", policy.TransportMode, RuntimeCallTransportModeNative)
	}
	if policy.ToolTransportMode() != "native" {
		t.Fatalf("ToolTransportMode = %q, want native", policy.ToolTransportMode())
	}
	if policy.TextualToolParser {
		t.Fatalf("TextualToolParser = true, want false")
	}
}

func TestDeriveRuntimeCallPolicyForceNativeOverridesBackendDefault(t *testing.T) {
	policy := DeriveRuntimeCallPolicy(RuntimeCallPolicyInput{
		SupportsNativeTools: false,
		ForceNativeTools:    true,
		ToolChoice:          "required",
		MaxTokens:           4096,
	})

	if policy.TransportMode != RuntimeCallTransportModeNative {
		t.Fatalf("TransportMode = %q, want %q", policy.TransportMode, RuntimeCallTransportModeNative)
	}
	if policy.ToolTransportMode() != "native" {
		t.Fatalf("ToolTransportMode = %q, want native", policy.ToolTransportMode())
	}
	if policy.TextualToolParser {
		t.Fatalf("TextualToolParser = true, want false for forced native transport")
	}
}
