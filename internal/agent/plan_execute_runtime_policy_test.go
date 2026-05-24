package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"idiom/internal/frontier/workerruntime"
	"idiom/internal/llama"
	"idiom/internal/workspace"
)

func TestPlanExecuteRuntimeCallPolicyNativeSubmitReady(t *testing.T) {
	opts := llama.ChatOptions{
		Tools: []llama.Tool{{
			Function: llama.ToolFunction{Name: "submit_plan"},
		}},
		ToolChoice: "required",
		MaxTokens:  4096,
	}

	policy := PlanExecuteRuntimeCallPolicyFromChatOptions(opts, true, true)
	if policy.TransportMode != PlanExecuteTransportModeNative {
		t.Fatalf("TransportMode = %q, want %q", policy.TransportMode, PlanExecuteTransportModeNative)
	}
	if policy.TextualToolParser {
		t.Fatalf("native submit-ready policy should not use textual parser")
	}
	if policy.ResponseFormatJSON {
		t.Fatalf("native submit-ready policy should not request JSON response format")
	}
	if !policy.ToolChoiceRequired || policy.ToolChoice != "required" {
		t.Fatalf("tool choice not preserved: %#v", policy)
	}
	if policy.PlanningTransport().EffectiveMaxTokens != 4096 {
		t.Fatalf("EffectiveMaxTokens = %d, want 4096", policy.PlanningTransport().EffectiveMaxTokens)
	}
}

func TestPlanExecuteRuntimeCallPolicyTextualJSONSubmitReady(t *testing.T) {
	opts := llama.ChatOptions{
		ResponseFormatJSON: true,
		MaxTokens:          4096,
	}

	policy := PlanExecuteRuntimeCallPolicyFromChatOptions(opts, false, true)
	if policy.TransportMode != PlanExecuteTransportModeTextualJSON {
		t.Fatalf("TransportMode = %q, want %q", policy.TransportMode, PlanExecuteTransportModeTextualJSON)
	}
	if !policy.TextualToolParser {
		t.Fatalf("textual JSON submit-ready policy should use textual parser")
	}
	if !policy.ResponseFormatJSON {
		t.Fatalf("textual JSON submit-ready policy should preserve response format")
	}
	if policy.PlanningTransport().EffectiveMaxTokens != 4096 {
		t.Fatalf("EffectiveMaxTokens = %d, want 4096", policy.PlanningTransport().EffectiveMaxTokens)
	}
}

func TestPlanExecuteRuntimeCallPolicyForceNativeOverridesBackendDefault(t *testing.T) {
	opts := llama.ChatOptions{
		Tools: []llama.Tool{{
			Function: llama.ToolFunction{Name: "submit_plan"},
		}},
		ToolChoice:       "required",
		ForceNativeTools: true,
		MaxTokens:        4096,
	}

	policy := PlanExecuteRuntimeCallPolicyFromChatOptions(opts, false, true)
	if policy.TransportMode != PlanExecuteTransportModeNative {
		t.Fatalf("TransportMode = %q, want %q", policy.TransportMode, PlanExecuteTransportModeNative)
	}
	if policy.TextualToolParser {
		t.Fatalf("forced native submit-ready policy should not use textual parser")
	}
	if policy.PhaseTransportMode() != "native" {
		t.Fatalf("PhaseTransportMode = %q, want native", policy.PhaseTransportMode())
	}
}

func TestPlanExecuteRuntimeCallPolicyMapsToGenericPolicy(t *testing.T) {
	thinkingOff := false
	policy := DerivePlanExecuteRuntimeCallPolicy(PlanExecuteRuntimeCallPolicyInput{
		SupportsNativeTools:      true,
		ParserContract:           " submit_plan_json ",
		ResponseFormat:           " json ",
		Temperature:              0.2,
		TopP:                     1,
		TopK:                     20,
		MinP:                     0.05,
		PresencePenalty:          0.1,
		RepetitionPenalty:        1.1,
		ToolChoice:               " required ",
		ThinkingPolicy:           " disabled_visible ",
		EnableThinking:           &thinkingOff,
		PreserveThinking:         false,
		ThinkingBudget:           64,
		ChatTemplateKwargs:       true,
		MaxTokens:                1024,
		TimeoutMillis:            1500,
		ExpressibleActionClasses: []string{actionSubmitPlan, actionSourceSatisfied, actionBlocker},
	})

	generic := policy.RuntimeCallPolicy()
	if generic.TransportMode != RuntimeCallTransportModeTextualJSON {
		t.Fatalf("generic TransportMode = %q, want %q", generic.TransportMode, RuntimeCallTransportModeTextualJSON)
	}
	if generic.ToolTransportMode() != policy.PhaseTransportMode() {
		t.Fatalf("transport projection mismatch: generic=%q plan=%q", generic.ToolTransportMode(), policy.PhaseTransportMode())
	}
	if generic.ParserContract != policy.ParserContract {
		t.Fatalf("ParserContract mismatch: generic=%q plan=%q", generic.ParserContract, policy.ParserContract)
	}
	if generic.ResponseFormatMode() != policy.ResponseFormatMode() {
		t.Fatalf("ResponseFormatMode mismatch: generic=%q plan=%q", generic.ResponseFormatMode(), policy.ResponseFormatMode())
	}
	if generic.ToolChoice != policy.ToolChoice || generic.ToolChoiceRequired != policy.ToolChoiceRequired {
		t.Fatalf("ToolChoice mismatch: generic=%q/%v plan=%q/%v", generic.ToolChoice, generic.ToolChoiceRequired, policy.ToolChoice, policy.ToolChoiceRequired)
	}
	if generic.ThinkingPolicy != policy.ThinkingPolicy || generic.MaxTokens != policy.MaxTokens || generic.TimeoutMillis != policy.TimeoutMillis {
		t.Fatalf("runtime limits mismatch: generic=%#v plan=%#v", generic, policy)
	}
	if generic.Temperature != policy.Temperature || generic.TopP != policy.TopP || generic.TopK != policy.TopK || generic.MinP != policy.MinP || generic.PresencePenalty != policy.PresencePenalty || generic.RepetitionPenalty != policy.RepetitionPenalty {
		t.Fatalf("runtime sampling mismatch: generic=%#v plan=%#v", generic, policy)
	}
	assertEqualSet(t, generic.ExpressibleActionClasses, []string{actionSubmitPlan, actionSourceSatisfied, actionBlocker})
}

func TestPlanExecuteRuntimeCallPolicySourceRefreshKeepsSmallVisibleToolBudget(t *testing.T) {
	thinkingOn := true
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        PlanThenExecutePlannerTaskFamily,
		RequiredEditPaths: []string{"a.go", "b.go"},
	}
	results := []workspace.ToolResult{
		{Tool: "code_navigate", Summary: "Code navigation routed read_symbol via go_read_symbol", Output: "route_summary: Read A in a.go lines 1-10\n\n1:func A() {}\n"},
		{Tool: "code_navigate", Summary: "Code navigation routed file_outline via go_file_outline", Output: "route_summary: Outlined 2 top-level declaration(s) in b.go.\n"},
	}
	opts := planThenExecuteUnreadRequiredSourceChatOptions(llama.ChatOptions{
		Tools: []llama.Tool{
			{Function: llama.ToolFunction{Name: "read_file"}},
			{Function: llama.ToolFunction{Name: "code_navigate"}},
			{Function: llama.ToolFunction{Name: "submit_plan"}},
		},
		EnableThinking:   &thinkingOn,
		PreserveThinking: true,
		MaxTokens:        16384,
	}, brief, results)

	policy := PlanExecuteRuntimeCallPolicyFromChatOptions(opts, true, false)
	if policy.TransportMode != PlanExecuteTransportModeNative {
		t.Fatalf("source-refresh transport = %q, want %q", policy.TransportMode, PlanExecuteTransportModeNative)
	}
	if policy.ResponseFormatJSON {
		t.Fatalf("source-refresh policy should not request JSON response format")
	}
	if policy.PlanningTransport().EffectiveMaxTokens != 768 {
		t.Fatalf("source-refresh EffectiveMaxTokens = %d, want 768", policy.PlanningTransport().EffectiveMaxTokens)
	}
	if opts.EnableThinking == nil || *opts.EnableThinking {
		t.Fatalf("source-refresh policy should force visible no-thinking tool output")
	}
}

func TestPlanExecuteRuntimeModelCallTelemetryIsBounded(t *testing.T) {
	thinkingOff := false
	var event RunEvent
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: PlanThenExecutePlannerTaskFamily,
		},
		OnEvent: func(emitted RunEvent) {
			event = emitted
		},
	}

	runner.emitModelCallTraceWithOptions(
		3,
		"main_loop",
		[]llama.ChatMessage{{Role: "user", Content: "plan edits"}},
		llama.ChatMessage{
			Content:      "{}",
			FinishReason: "length",
			Usage:        llama.Usage{PromptTokens: 12, CompletionTokens: 7, TotalTokens: 19},
		},
		250,
		llama.ChatOptions{
			Model:              "/private/models/qwen-local.gguf",
			ResponseFormatJSON: true,
			EnableThinking:     &thinkingOff,
			ThinkingBudget:     512,
			ChatTemplateKwargs: true,
			Temperature:        0.2,
			TopP:               1,
			TopK:               20,
			MinP:               0.05,
			PresencePenalty:    0.1,
			RepetitionPenalty:  1.1,
			MaxTokens:          4096,
		},
		false,
		nil,
	)

	if event.Role != "model_call" {
		t.Fatalf("event role = %q, want model_call", event.Role)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(event.Content), &payload); err != nil {
		t.Fatalf("decode model_call event: %v", err)
	}
	assertStringField(t, payload, "model_alias", "qwen-local.gguf")
	assertStringField(t, payload, "runtime_transport_mode", string(PlanExecuteTransportModeTextualJSON))
	assertStringField(t, payload, "response_format", "json")
	assertStringField(t, payload, "response_format_mode", "json")
	assertStringField(t, payload, "finish_reason", "length")
	assertNumberField(t, payload, "prompt_tokens", 12)
	assertNumberField(t, payload, "completion_tokens", 7)
	assertNumberField(t, payload, "total_tokens", 19)
	assertNumberField(t, payload, "effective_max_tokens", 4096)
	assertNumberField(t, payload, "temperature", 0.2)
	assertNumberField(t, payload, "top_p", 1)
	assertNumberField(t, payload, "top_k", 20)
	assertNumberField(t, payload, "min_p", 0.05)
	assertNumberField(t, payload, "presence_penalty", 0.1)
	assertNumberField(t, payload, "repetition_penalty", 1.1)
	assertNumberField(t, payload, "thinking_budget", 512)
	if got, ok := payload["thinking_enabled"].(bool); !ok || got {
		t.Fatalf("thinking_enabled = %#v, want false", payload["thinking_enabled"])
	}
	if got, ok := payload["chat_template_kwargs"].(bool); !ok || !got {
		t.Fatalf("chat_template_kwargs = %#v, want true", payload["chat_template_kwargs"])
	}
	for _, key := range []string{
		"backend_capability_native_tools",
		"backend_capability_json_object",
		"backend_capability_strict_json_schema",
		"backend_capability_grammar",
		"backend_capability_thinking_controls",
		"backend_capability_request_timeout_telemetry",
	} {
		if _, ok := payload[key].(bool); !ok {
			t.Fatalf("model_call telemetry missing boolean backend capability %q in %#v", key, payload)
		}
	}
	if strings.Contains(event.Content, "/private/models") {
		t.Fatalf("model_call telemetry leaked raw model path: %s", event.Content)
	}
}

func TestToolRequestNormalizerClassifiesParserFailure(t *testing.T) {
	result := normalizeToolRequestSyntax(llama.ChatMessage{ToolCalls: []llama.ToolCall{{
		Function: llama.ToolCallFunc{
			Name:      "submit_plan",
			Arguments: `{"edits":`,
		},
	}}})
	if result.OK {
		t.Fatal("malformed native tool arguments should not parse")
	}
	if result.ParserFailureClass != "malformed_json" {
		t.Fatalf("ParserFailureClass = %q, want malformed_json", result.ParserFailureClass)
	}
	if result.EventTransport != toolRequestNormalizerTransportNative {
		t.Fatalf("EventTransport = %q, want native", result.EventTransport)
	}
}

func TestToolRequestNormalizerParsesIdiomToolCLIShape(t *testing.T) {
	result := normalizeToolRequestSyntax(llama.ChatMessage{Content: `idiom tool task {"tool":"task","agent":"explorer","mode":"read_only","prompt":"Inspect subagent flow."}`})
	if !result.OK {
		t.Fatalf("expected idiom tool CLI shape to parse, outcome=%s raw=%s", result.EventOutcome, result.Raw)
	}
	if result.EventTransport != toolRequestNormalizerTransportIdiomCLI {
		t.Fatalf("EventTransport = %q, want %q", result.EventTransport, toolRequestNormalizerTransportIdiomCLI)
	}
	if result.Request.Tool != "task" || result.Request.Agent != "explorer" || result.Request.Mode != "read_only" {
		t.Fatalf("unexpected parsed request: %#v", result.Request)
	}
}

func assertStringField(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got, ok := payload[key].(string); !ok || got != want {
		t.Fatalf("%s = %#v, want %q", key, payload[key], want)
	}
}

func assertNumberField(t *testing.T, payload map[string]any, key string, want float64) {
	t.Helper()
	if got, ok := payload[key].(float64); !ok || got != want {
		t.Fatalf("%s = %#v, want %v", key, payload[key], want)
	}
}
