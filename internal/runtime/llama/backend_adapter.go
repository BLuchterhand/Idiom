package llama

import (
	"strings"

	"idiom/internal/platform/config"
)

type backendProtocol string

const (
	backendProtocolChatCompletions backendProtocol = "chat_completions"
	backendProtocolResponses       backendProtocol = "responses"
	backendProtocolMessages        backendProtocol = "messages"
)

const anthropicVersion = "2023-06-01"

type backendRequestSettings struct {
	ReasoningEffort string
	Verbosity       string
}

type backendChatPayload struct {
	Body            any
	ToolNameReverse map[string]string
}

type backendAdapter interface {
	Kind() string
	Protocol() backendProtocol
	ModelsPath() string
	RequiresAuth() bool
	DefaultAPIKeyEnv() string
	AuthHeaders(string) map[string]string
	StructuredOutputCapabilities() StructuredOutputCapabilities
}

type bearerAuth struct{}

func (bearerAuth) AuthHeaders(apiKey string) map[string]string {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil
	}

	return map[string]string{"Authorization": "Bearer " + apiKey}
}

type chatCompletionsBackendAdapter interface {
	backendAdapter
	ChatCompletionsPath() string
	ChatMessagesForRequest([]ChatMessage, ChatOptions) []ChatMessage
	ChatCompletionsPayload([]ChatMessage, ChatOptions, bool, backendRequestSettings) backendChatPayload
}

type responsesBackendAdapter interface {
	backendAdapter
	ResponsesPayload([]ChatMessage, ChatOptions, backendRequestSettings) (responsesRequest, bool)
}

type messagesBackendAdapter interface {
	backendAdapter
	MessagesPath() string
	MessagesPayload([]ChatMessage, ChatOptions, bool, backendRequestSettings) backendChatPayload
	DecodeMessagesResponse([]byte, map[string]string) (ChatMessage, error)
}

var backendAdapters = map[string]backendAdapter{
	config.BackendLlama: localChatAdapter{
		kind:        config.BackendLlama,
		nativeTools: true,
	},
	config.BackendMLX: localChatAdapter{
		kind:        config.BackendMLX,
		nativeTools: false,
	},
	config.BackendMiniMax: hostedChatAdapter{
		kind:             config.BackendMiniMax,
		defaultAPIKeyEnv: "MINIMAX_API_KEY",
	},
	config.BackendZAI: hostedChatAdapter{
		kind:             config.BackendZAI,
		modelsPath:       "/models",
		chatPath:         "/chat/completions",
		defaultAPIKeyEnv: "ZAI_API_KEY",
		thinkingToggle:   true,
		reasoningEffort:  true,
	},
	config.BackendGLM: hostedChatAdapter{
		kind:             config.BackendGLM,
		modelsPath:       "/models",
		chatPath:         "/chat/completions",
		defaultAPIKeyEnv: "ZAI_API_KEY",
		thinkingToggle:   true,
		reasoningEffort:  true,
	},
	config.BackendDeepSeek: hostedChatAdapter{
		kind:              config.BackendDeepSeek,
		defaultAPIKeyEnv:  "DEEPSEEK_API_KEY",
		thinkingToggle:    true,
		preserveReasoning: true,
	},
	config.BackendOpenAIResponses: responsesAdapter{
		kind:             config.BackendOpenAIResponses,
		defaultAPIKeyEnv: "OPENAI_API_KEY",
	},
	config.BackendCodex: responsesAdapter{
		kind:             config.BackendCodex,
		defaultAPIKeyEnv: "OPENAI_API_KEY",
	},
	config.BackendAnthropic: anthropicAdapter{},
}

func normalizeBackendKind(backend string) string {
	kind := strings.ToLower(strings.TrimSpace(backend))
	if kind == "" {
		return config.BackendLlama
	}

	if _, ok := backendAdapters[kind]; ok {
		return kind
	}

	return config.BackendLlama
}

func backendAdapterFor(backend string) backendAdapter {
	return backendAdapters[normalizeBackendKind(backend)]
}

func lookupBackendAdapter(backend string) (backendAdapter, bool) {
	kind := strings.ToLower(strings.TrimSpace(backend))
	if kind == "" {
		kind = config.BackendLlama
	}

	adapter, ok := backendAdapters[kind]

	return adapter, ok
}

func chatMessagesForRequest(messages []ChatMessage, opts ChatOptions, adapter backendAdapter) []ChatMessage {
	if projector, ok := adapter.(interface {
		ChatMessagesForRequest([]ChatMessage, ChatOptions) []ChatMessage
	}); ok {
		return projector.ChatMessagesForRequest(messages, opts)
	}

	return chatMessagesForBackend(messages, opts, adapter)
}

// PrepareChatMessagesForBackend applies backend-owned request-history shaping
// without requiring callers outside this package to know adapter internals.
func PrepareChatMessagesForBackend(messages []ChatMessage, opts ChatOptions, backend string) []ChatMessage {
	return chatMessagesForRequest(messages, opts, backendAdapterFor(backend))
}

func chatMessagesForBackend(messages []ChatMessage, opts ChatOptions, adapter backendAdapter) []ChatMessage {
	if len(messages) == 0 {
		return nil
	}

	if opts.PreserveThinking {
		return messages
	}

	out := make([]ChatMessage, len(messages))
	copy(out, messages)

	preserveAssistantReasoning := preservesAssistantReasoningContent(adapter)
	for i := range out {
		if preserveAssistantReasoning && strings.TrimSpace(out[i].Role) == "assistant" {
			continue
		}

		out[i].ReasoningContent = ""
	}

	return out
}

// PrepareAssistantHistoryMessageForBackend applies backend reasoning-history
// ownership to a single assistant history item before it is appended to a turn.
func PrepareAssistantHistoryMessageForBackend(message ChatMessage, opts ChatOptions, backend string) ChatMessage {
	if opts.PreserveThinking {
		return message
	}

	if preservesAssistantReasoningContent(backendAdapterFor(backend)) && strings.TrimSpace(message.Role) == "assistant" {
		return message
	}

	message.ReasoningContent = ""

	return message
}

func preservesAssistantReasoningContent(adapter backendAdapter) bool {
	hosted, ok := adapter.(hostedChatAdapter)

	return ok && hosted.preserveReasoning
}

type localChatAdapter struct {
	kind        string
	nativeTools bool
}

func (a localChatAdapter) Kind() string { return a.kind }

func (a localChatAdapter) Protocol() backendProtocol { return backendProtocolChatCompletions }

func (a localChatAdapter) ModelsPath() string { return "/v1/models" }

func (a localChatAdapter) ChatCompletionsPath() string { return "/v1/chat/completions" }

func (a localChatAdapter) RequiresAuth() bool { return false }

func (a localChatAdapter) DefaultAPIKeyEnv() string { return "" }

func (a localChatAdapter) AuthHeaders(_ string) map[string]string { return nil }

func (a localChatAdapter) StructuredOutputCapabilities() StructuredOutputCapabilities {
	return StructuredOutputCapabilities{
		NativeTools:      a.nativeTools,
		JSONObject:       true,
		ThinkingControls: true,
	}
}

func (a localChatAdapter) chatTemplateKwargs(opts ChatOptions) *chatTemplateKwargs {
	// llama.cpp accepts request-level chat_template_kwargs. MLX deployments
	// commonly bake thinking defaults into server-start chat-template args, so
	// per-request kwargs are sent only when explicitly requested.
	if a.kind == config.BackendMLX && !opts.ChatTemplateKwargs {
		return nil
	}

	if opts.EnableThinking == nil && !opts.PreserveThinking && opts.ThinkingBudget <= 0 {
		return nil
	}

	return &chatTemplateKwargs{
		EnableThinking:   clonePtr(opts.EnableThinking),
		PreserveThinking: opts.PreserveThinking,
		ThinkingBudget:   opts.ThinkingBudget,
	}
}

func (a localChatAdapter) ChatMessagesForRequest(messages []ChatMessage, opts ChatOptions) []ChatMessage {
	prepared := repairMessageToolPairing(messages)
	if BackendModelCapabilityProfile(a.kind, opts.Model).ChatTemplate.SystemMessagesMustLead == CapabilitySupported {
		prepared = normalizeMLXSystemMessages(prepared)
	}

	return chatMessagesForBackend(prepared, opts, a)
}

// normalizeMLXSystemMessages adapts Idiom's incremental system guidance to
// MLX chat templates that require every system message to be the first
// message. Idiom may append system guidance after tool results or an
// assistant turn; Qwen3.5/Qwen3.8's template rejects that otherwise valid
// history. Keeping the guidance in order at the front preserves all of it
// while satisfying the backend's protocol constraint.
func normalizeMLXSystemMessages(messages []ChatMessage) []ChatMessage {
	var systemContent strings.Builder
	otherMessages := make([]ChatMessage, 0, len(messages))
	foundSystem := false

	for _, message := range messages {
		if !strings.EqualFold(strings.TrimSpace(message.Role), "system") {
			otherMessages = append(otherMessages, message)
			continue
		}

		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}

		if foundSystem {
			systemContent.WriteString("\n\n")
		}
		systemContent.WriteString(content)
		foundSystem = true
	}

	if !foundSystem {
		return messages
	}

	return append([]ChatMessage{{Role: "system", Content: systemContent.String()}}, otherMessages...)
}

func (a localChatAdapter) ChatCompletionsPayload(messages []ChatMessage, opts ChatOptions, stream bool, _ backendRequestSettings) backendChatPayload {
	req := chatRequest{
		Model:              opts.Model,
		Messages:           a.ChatMessagesForRequest(messages, opts),
		Stream:             stream,
		Temperature:        opts.Temperature,
		TopP:               opts.TopP,
		TopK:               opts.TopK,
		MinP:               opts.MinP,
		PresencePenalty:    opts.PresencePenalty,
		RepeatPenalty:      opts.RepetitionPenalty,
		RepetitionPenalty:  opts.RepetitionPenalty,
		ChatTemplateKwargs: a.chatTemplateKwargs(opts),
		MaxTokens:          opts.MaxTokens,
		Tools:              opts.Tools,
		ToolChoice:         opts.ToolChoice,
	}

	if opts.ResponseFormatJSON {
		req.ResponseFormat = map[string]string{"type": "json_object"}
	}

	if !a.nativeTools && !opts.ForceNativeTools {
		req.Tools = nil
		req.ToolChoice = ""
	}

	return backendChatPayload{Body: req}
}

type hostedChatAdapter struct {
	bearerAuth
	kind              string
	modelsPath        string
	chatPath          string
	defaultAPIKeyEnv  string
	thinkingToggle    bool
	reasoningEffort   bool
	preserveReasoning bool
}

func (a hostedChatAdapter) Kind() string { return a.kind }

func (a hostedChatAdapter) Protocol() backendProtocol { return backendProtocolChatCompletions }

func (a hostedChatAdapter) ModelsPath() string {
	if strings.TrimSpace(a.modelsPath) != "" {
		return a.modelsPath
	}

	return "/v1/models"
}

func (a hostedChatAdapter) ChatCompletionsPath() string {
	if strings.TrimSpace(a.chatPath) != "" {
		return a.chatPath
	}

	return "/v1/chat/completions"
}

func (a hostedChatAdapter) RequiresAuth() bool { return true }

func (a hostedChatAdapter) DefaultAPIKeyEnv() string { return a.defaultAPIKeyEnv }

func (a hostedChatAdapter) StructuredOutputCapabilities() StructuredOutputCapabilities {
	return StructuredOutputCapabilities{
		NativeTools:      true,
		JSONObject:       true,
		ThinkingControls: a.thinkingToggle || a.reasoningEffort,
	}
}

func (a hostedChatAdapter) ChatCompletionsPayload(messages []ChatMessage, opts ChatOptions, stream bool, settings backendRequestSettings) backendChatPayload {
	tools, reverse := normalizeHostedTools(opts.Tools)
	payload := hostedChatPayload{
		Model:    opts.Model,
		Messages: a.ChatMessagesForRequest(messages, opts),
		Stream:   stream,
	}
	if opts.MaxTokens > 0 {
		payload.MaxTokens = opts.MaxTokens
	}

	if opts.Temperature > 0 {
		payload.Temperature = opts.Temperature
	}

	if opts.TopP > 0 {
		payload.TopP = opts.TopP
	}

	if opts.PresencePenalty != 0 {
		payload.PresencePenalty = opts.PresencePenalty
	}

	if len(tools) > 0 {
		payload.Tools = tools
		payload.ToolNameReverse = reverse
	}

	if opts.ToolChoice != "" {
		payload.ToolChoice = opts.ToolChoice
	}

	if a.thinkingToggle && opts.EnableThinking != nil {
		mode := "disabled"
		if *opts.EnableThinking {
			mode = "enabled"
		}

		payload.Thinking = map[string]string{"type": mode}
	}

	if a.reasoningEffort {
		payload.ReasoningEffort = strings.TrimSpace(settings.ReasoningEffort)
	}

	if opts.ResponseFormatJSON {
		payload.ResponseFormat = map[string]string{"type": "json_object"}
	}

	return backendChatPayload{Body: payload, ToolNameReverse: reverse}
}

func (a hostedChatAdapter) ChatMessagesForRequest(messages []ChatMessage, _ ChatOptions) []ChatMessage {
	if len(messages) == 0 {
		return nil
	}

	out := repairMessageToolPairing(messages)
	for i, msg := range out {
		if !a.preserveReasoning || msg.Role != "assistant" {
			msg.ReasoningContent = ""
		}

		out[i] = msg
	}

	return out
}

type responsesAdapter struct {
	bearerAuth
	kind             string
	defaultAPIKeyEnv string
}

func (a responsesAdapter) Kind() string { return a.kind }

func (a responsesAdapter) Protocol() backendProtocol { return backendProtocolResponses }

func (a responsesAdapter) ModelsPath() string { return "/v1/models" }

func (a responsesAdapter) RequiresAuth() bool { return true }

func (a responsesAdapter) DefaultAPIKeyEnv() string { return a.defaultAPIKeyEnv }

func (a responsesAdapter) StructuredOutputCapabilities() StructuredOutputCapabilities {
	return StructuredOutputCapabilities{
		NativeTools: true,
		JSONObject:  true,
	}
}

func (a responsesAdapter) ResponsesPayload(messages []ChatMessage, opts ChatOptions, settings backendRequestSettings) (responsesRequest, bool) {
	temp := opts.Temperature
	topP := opts.TopP
	payload := responsesRequest{
		Model:           opts.Model,
		Input:           responsesInputFromMessages(messages),
		Store:           false,
		Tools:           responsesToolsFromChatTools(opts.Tools),
		ToolChoice:      opts.ToolChoice,
		MaxOutputTokens: opts.MaxTokens,
	}

	if opts.Temperature > 0 {
		payload.Temperature = &temp
	}

	if opts.TopP > 0 {
		payload.TopP = &topP
	}

	if settings.ReasoningEffort != "" {
		payload.Reasoning = &responsesReasoning{Effort: settings.ReasoningEffort}
	}

	if settings.Verbosity != "" {
		payload.Text = &responsesText{Verbosity: settings.Verbosity}
	}

	return payload, true
}
