package app

import (
	"fmt"
	"strings"
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
