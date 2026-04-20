package frontier

import "strings"

type EffortPlan struct {
	Lane         Lane
	FollowupLane Lane
	Thoroughness string
}

type EffortPolicySnapshot struct {
	Lane                 string `json:"lane,omitempty"`
	Level                string `json:"level,omitempty"`
	MaxToolSteps         int    `json:"max_tool_steps,omitempty"`
	AnalysisToolBudget   int    `json:"analysis_tool_budget,omitempty"`
	ImplementToolBudget  int    `json:"implement_tool_budget,omitempty"`
	DeepeningPasses      int    `json:"deepening_passes,omitempty"`
	TimeBudgetSeconds    int    `json:"time_budget_seconds,omitempty"`
	MaxOutputTokens      int    `json:"max_output_tokens,omitempty"`
	ReasoningBudget      int    `json:"reasoning_budget,omitempty"`
	ConfidenceFloor      string `json:"confidence_floor,omitempty"`
	BudgetExhaustionMode string `json:"budget_exhaustion_mode,omitempty"`
}

func BuildEffortPolicySnapshot(plan EffortPlan, maxToolSteps int, timeoutSeconds int, maxOutputTokens int, reasoningBudget int) EffortPolicySnapshot {
	level := EffortLevelForPlan(plan)
	policy := EffortPolicySnapshot{
		Lane:                 string(plan.Lane),
		Level:                level,
		MaxToolSteps:         maxToolSteps,
		TimeBudgetSeconds:    timeoutSeconds,
		MaxOutputTokens:      maxOutputTokens,
		ReasoningBudget:      reasoningBudget,
		ConfidenceFloor:      ConfidenceFloorForEffort(level),
		BudgetExhaustionMode: "answer_with_explicit_uncertainty_or_fallback",
	}
	switch plan.Lane {
	case LaneExplore, LaneReview, LaneArchitecture:
		policy.AnalysisToolBudget = ExploreToolBudget(maxToolSteps)
		policy.DeepeningPasses = 2
	case LaneImplement:
		policy.ImplementToolBudget = ImplementToolBudget(maxToolSteps)
	case LaneLocate:
		policy.AnalysisToolBudget = ExploreToolBudget(maxToolSteps)
		if plan.FollowupLane == LaneImplement {
			policy.ImplementToolBudget = ImplementToolBudget(maxToolSteps)
		}
	default:
		policy.AnalysisToolBudget = maxToolSteps
	}
	if plan.FollowupLane == LaneImplement && policy.ImplementToolBudget == 0 {
		policy.ImplementToolBudget = ImplementToolBudget(maxToolSteps)
	}
	return policy
}

func EffortLevelForPlan(plan EffortPlan) string {
	if strings.TrimSpace(plan.Thoroughness) != "" {
		return plan.Thoroughness
	}
	switch plan.Lane {
	case LaneImplement:
		return "bounded"
	case LaneMain:
		return "standard"
	default:
		return "medium"
	}
}

func ConfidenceFloorForEffort(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "quick":
		return "low_with_explicit_uncertainty"
	case "thorough":
		return "high_or_exhausted_budget"
	case "bounded":
		return "verified_or_failed"
	default:
		return "medium"
	}
}

func ExploreToolBudget(maxToolSteps int) int {
	switch {
	case maxToolSteps <= 0:
		return 12
	case maxToolSteps < 6:
		return maxToolSteps
	default:
		return minInt(maxToolSteps, 12)
	}
}

func ImplementToolBudget(maxToolSteps int) int {
	switch {
	case maxToolSteps <= 0:
		return 14
	case maxToolSteps < 6:
		return maxToolSteps
	default:
		return minInt(maxToolSteps, 14)
	}
}

func ImplementToolBudgetForRequest(maxToolSteps int, prompt string, packet TargetPacket) int {
	budget := ImplementToolBudget(maxToolSteps)
	if !RequestNeedsExpandedImplementBudget(prompt, packet) {
		return budget
	}
	if maxToolSteps > budget {
		budget = maxToolSteps
	}
	budget = maxInt(budget, 24)
	if PromptRequestsWholeFileRefactor(prompt) {
		budget = maxInt(budget, 32)
	}
	if count := ExplicitGoMoveBatchHintCount(prompt); count > 0 {
		budget = maxInt(budget, 8+count*3)
	}
	if len(packet.PrimaryPaths) > 1 {
		budget = maxInt(budget, 16+len(packet.PrimaryPaths)*2)
	}
	return minInt(budget, 48)
}

func RequestNeedsExpandedImplementBudget(prompt string, packet TargetPacket) bool {
	normalized := strings.ToLower(prompt)
	if PromptRequestsWholeFileRefactor(prompt) || TargetPacketNeedsStructuralPolicy(packet) {
		return true
	}
	return ContainsAny(normalized,
		"go_move_declarations",
		"go_file_outline",
		"large go refactor",
		"giant file",
		"monolithic file",
		"split the file",
		"split into",
		"multi-file refactor",
		"across multiple files",
	)
}

func ExplicitGoMoveBatchHintCount(prompt string) int {
	normalized := strings.ToLower(prompt)
	tags := []string{
		"tag:model_state",
		"tag:lifecycle",
		"tag:prompt_routing",
		"tag:rendering",
		"tag:task_memory",
		"tag:implementation",
		"tag:write_guard",
		"tag:analysis_agents",
		"tag:control_rails",
	}
	count := 0
	for _, tag := range tags {
		if strings.Contains(normalized, tag) {
			count++
		}
	}
	if count > 0 {
		return count
	}
	return strings.Count(normalized, "destination:")
}

func LaneUsesThoroughness(lane Lane) bool {
	switch lane {
	case LaneExplore, LaneReview, LaneArchitecture:
		return true
	default:
		return false
	}
}

func RouteContinuesToImplement(lane Lane, followupLane Lane) bool {
	return followupLane == LaneImplement
}

func DefaultExecutionPlanSummary(plan EffortPlan) string {
	switch plan.Lane {
	case LaneReview:
		if RouteContinuesToImplement(plan.Lane, plan.FollowupLane) {
			return "Reviewing bounded evidence before the implementation pass."
		}
		return "Reviewing bounded evidence and ranking likely findings."
	case LaneArchitecture:
		if RouteContinuesToImplement(plan.Lane, plan.FollowupLane) {
			return "Mapping structural boundaries before the implementation pass."
		}
		return "Mapping structural boundaries and ranking architecture hotspots."
	case LaneExplore:
		if RouteContinuesToImplement(plan.Lane, plan.FollowupLane) {
			return "Exploring the repository before the implementation pass."
		}
		return "Exploring the repository and gathering focused context."
	case LaneLocate:
		return "Locating the exact code path before applying the requested edit."
	case LaneImplement:
		return "Preparing a bounded implementation plan against the targeted files."
	default:
		return "Waiting for the response."
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
