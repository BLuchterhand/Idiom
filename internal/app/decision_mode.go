package app

import (
	"fmt"
	"strings"

	"qwen-bubbles/internal/frontier"
)

type agentDecisionMode string

const (
	agentDecisionModeAuto    agentDecisionMode = "auto"
	agentDecisionModePlan    agentDecisionMode = "plan"
	agentDecisionModeBuild   agentDecisionMode = "build"
	agentDecisionModeReview  agentDecisionMode = "review"
	agentDecisionModeExplore agentDecisionMode = "explore"
)

func normalizeAgentDecisionMode(value string) (agentDecisionMode, error) {
	mode := agentDecisionMode(strings.ToLower(strings.TrimSpace(value)))
	switch mode {
	case "", agentDecisionModeAuto:
		return agentDecisionModeAuto, nil
	case agentDecisionModePlan, agentDecisionModeBuild, agentDecisionModeReview, agentDecisionModeExplore:
		return mode, nil
	default:
		return "", fmt.Errorf("unsupported agent mode %q", value)
	}
}

func (m *Model) currentAgentDecisionMode() agentDecisionMode {
	if m == nil || m.agentMode == "" {
		return agentDecisionModeAuto
	}
	return m.agentMode
}

func (m *Model) applyAgentDecisionMode(prompt string, plan promptExecutionPlan) promptExecutionPlan {
	mode := m.currentAgentDecisionMode()
	defaultThoroughness := ""
	if m != nil && m.workspace != nil {
		defaultThoroughness = string(m.workspace.ExploreThoroughness())
	}
	return fromFrontierPromptPlan(frontier.ApplyAgentModeToPromptPlan(prompt, toFrontierPromptPlan(plan), string(mode), defaultThoroughness))
}
