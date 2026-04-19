package app

import (
	"fmt"
	"strings"

	"qwen-bubbles/internal/workspace"
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
	plan.agentMode = mode
	if mode == agentDecisionModeAuto || plan.lane == "" {
		return plan
	}
	profile := buildPromptRouteProfile(prompt)
	switch mode {
	case agentDecisionModePlan:
		plan.lane = analysisLaneArchitecture
		plan.followupLane = ""
		plan.reason = promptRouteReasonArchitectureRequest
		plan.contract = answerContractForLane(plan.lane)
		plan.intent.WantsPlanOnly = true
		plan.intent.WantsEdits = false
		plan.intent.Vetoes = uniqueNonEmptyStrings(append(plan.intent.Vetoes, "agent_plan_mode_blocks_edits"))
	case agentDecisionModeBuild:
		if profile.wantsVerificationOnly || profile.hasReadOnlyBoundary || plan.intent.WantsPlanOnly {
			plan.intent.Vetoes = uniqueNonEmptyStrings(append(plan.intent.Vetoes, "agent_build_mode_preserved_user_read_only_boundary"))
			break
		}
		plan.lane = analysisLaneImplement
		plan.followupLane = ""
		plan.reason = promptRouteReasonDirectImplementation
		plan.contract = answerContractForLane(plan.lane)
		plan.intent.WantsEdits = true
		plan.intent.WantsPlanOnly = false
	case agentDecisionModeReview:
		plan.lane = analysisLaneReview
		plan.followupLane = ""
		plan.reason = promptRouteReasonReviewRequest
		plan.contract = answerContractForLane(plan.lane)
		plan.intent.WantsEdits = false
	case agentDecisionModeExplore:
		plan.lane = analysisLaneExplore
		plan.followupLane = ""
		plan.reason = promptRouteReasonExploreRequest
		plan.contract = answerContractForLane(plan.lane)
		plan.intent.WantsEdits = false
	}
	if analysisLaneUsesThoroughness(plan.lane) && plan.thoroughness == "" {
		if m != nil && m.workspace != nil {
			plan.thoroughness = m.workspace.ExploreThoroughness()
		}
		if plan.thoroughness == "" {
			plan.thoroughness = workspace.ThoroughnessMedium
		}
	}
	plan.intent.SelectedLane = string(plan.lane)
	plan.intent.FollowupLane = string(plan.followupLane)
	plan.intent.Source = firstNonEmptyString(plan.intent.Source, "host_structured_intent") + "+agent_mode"
	return finalizePromptExecutionPlan(prompt, profile, plan)
}
