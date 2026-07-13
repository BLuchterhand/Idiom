package app

import (
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/runprofile"
	"idiom/internal/platform/config"
	"idiom/internal/platform/textutil"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/modepolicy"
	"idiom/internal/runtime/promptstack"
)

func activeModelDisplayID(displayID string, modelID string) string {
	return textutil.Fallback(strings.TrimSpace(displayID), strings.TrimSpace(modelID))
}

type promptCompositionDerivation struct {
	cacheKey     string
	mode         modepolicy.Mode
	modelFamily  string
	overlayModel string
}

type composedPromptBuild struct {
	text    string
	sources []promptstack.Source
}

type systemPromptFragments struct {
	Text    string
	Sources []promptstack.Source
}

func derivePromptComposition(
	activeMode runprofile.AgentMode,
	configuredMode runprofile.AgentMode,
	modelFamily string,
	plannerModel string,
	configuredModel string,
) promptCompositionDerivation {
	mode := currentMode(activeMode, configuredMode)
	overlayModel := textutil.FirstNonEmpty(plannerModel, configuredModel)

	return promptCompositionDerivation{
		cacheKey: strings.Join([]string{
			string(mode.Name),
			strings.TrimSpace(modelFamily),
			strings.TrimSpace(overlayModel),
		}, "\x00"),
		mode:         mode,
		modelFamily:  modelFamily,
		overlayModel: overlayModel,
	}
}

func buildComposedPrompt(stack promptstack.Stack, modeFragments systemPromptFragments) composedPromptBuild {
	parts := make([]string, 0, 2)
	if base := strings.TrimSpace(stack.Text); base != "" {
		parts = append(parts, base)
	}
	if extras := strings.TrimSpace(modeFragments.Text); extras != "" {
		parts = append(parts, extras)
	}

	sources := append([]promptstack.Source(nil), stack.Sources...)
	sources = append(sources, modeFragments.Sources...)

	return composedPromptBuild{
		text:    strings.Join(parts, "\n\n"),
		sources: sources,
	}
}

func (s *promptRuntimeCompletionState) resolveComposedPrompt(
	stack promptstack.Stack,
	derivation promptCompositionDerivation,
	loadModeFragments func([]promptstack.Source) systemPromptFragments,
) composedPromptBuild {
	if s == nil {
		return composedPromptBuild{}
	}
	if s.composedPromptCacheKey == derivation.cacheKey {
		return composedPromptBuild{
			text:    s.composedPrompt,
			sources: append([]promptstack.Source(nil), s.composedPromptSources...),
		}
	}

	stack = clonePromptStack(stack)
	stack = promptstack.AppendOverlays(
		stack,
		promptstack.ModelCompatibilityOverlays(derivation.modelFamily, derivation.overlayModel),
	)

	modeFragments := systemPromptFragments{}
	if loadModeFragments != nil {
		modeFragments = loadModeFragments(append([]promptstack.Source(nil), stack.Sources...))
	}
	result := buildComposedPrompt(stack, modeFragments)

	s.composedPrompt = result.text
	s.composedPromptSources = append([]promptstack.Source(nil), result.sources...)
	s.composedPromptCacheKey = derivation.cacheKey

	return composedPromptBuild{
		text:    result.text,
		sources: append([]promptstack.Source(nil), result.sources...),
	}
}

func (s *promptRuntimeCompletionState) invalidateComposedPrompt() {
	if s == nil {
		return
	}

	s.composedPrompt = ""
	s.composedPromptSources = nil
	s.composedPromptCacheKey = ""
}

func buildPromptStack(chat config.Chat, profiles []string) (promptstack.Stack, error) {
	stack, err := promptstack.Build(chat, profiles)
	if err == nil {
		return stack, nil
	}
	if !chat.AllowMissingPrompts {
		return promptstack.Stack{}, err
	}

	lenientStack, lenientErr := promptstack.BuildLenient(chat, profiles)
	if lenientErr != nil {
		return promptstack.Stack{}, err
	}
	lenientStack.Warnings = append([]string{"prompt context degraded: " + err.Error()}, lenientStack.Warnings...)

	return lenientStack, nil
}

func (s *promptRuntimeCompletionState) applyPromptStackBuild(stack promptstack.Stack) {
	if s == nil {
		return
	}

	s.promptStack = stack
	s.invalidateComposedPrompt()
}

func chatOptions(chat config.Chat, modelID string, modelThinkingFloor bool, disableThinkingOverride bool) llama.ChatOptions {
	enableThinking := cloneChatBoolPtr(chat.EnableThinking)
	preserveThinking := chat.PreserveThinking
	chatTemplateKwargs := enableThinking != nil && !*enableThinking
	if disableThinkingOverride {
		thinkingOff := false
		enableThinking = &thinkingOff
		preserveThinking = false
		chatTemplateKwargs = true
	}

	return llama.ChatOptions{
		Model:              modelID,
		Temperature:        chat.Temperature,
		TopP:               chat.TopP,
		TopK:               chat.TopK,
		MinP:               chat.MinP,
		PresencePenalty:    chat.PresencePenalty,
		RepetitionPenalty:  chat.RepetitionPenalty,
		EnableThinking:     enableThinking,
		PreserveThinking:   preserveThinking,
		ChatTemplateKwargs: chatTemplateKwargs,
		ModelThinkingFloor: modelThinkingFloor,
		MaxTokens:          chat.MaxTokens,
	}
}

func compactionContextSize(primaryContextSize int, classifierContextSize int) int {
	if primaryContextSize > 0 {
		return primaryContextSize
	}
	if classifierContextSize > 0 {
		return classifierContextSize
	}

	return 65536
}

func currentMode(activeMode runprofile.AgentMode, configuredMode runprofile.AgentMode) modepolicy.Mode {
	if activeMode != "" {
		return modepolicy.ModeOrDefault(string(activeMode))
	}

	return modepolicy.ModeOrDefault(string(configuredMode))
}

func currentServerStatusSource(backendRevision uint64, controller *llama.ServerController, plannerSpec config.LlamaServer) serverStatusSource {
	return serverStatusSource{
		backendRevision: backendRevision,
		controller:      controller,
		kind:            plannerSpec.Kind,
		endpoint:        plannerSpec.EndpointBaseURL(),
		model:           plannerSpec.Model,
		displayName:     plannerSpec.DisplayName,
	}
}

func resolveProjectRoot(configuredRoot string, workspaceManager *workspace.Manager) string {
	if root := strings.TrimSpace(configuredRoot); root != "" {
		return root
	}
	if workspaceManager != nil {
		return workspaceManager.Root()
	}

	return ""
}
