package featureplanner

import "strings"

type ToolName string

const (
	ToolSubmitFeaturePlan     ToolName = "submit_feature_plan"
	ToolSourceSatisfied       ToolName = "source_satisfied"
	ToolAskPlannerQuestion    ToolName = "ask_planner_question"
	ToolGitHistoryRequirement ToolName = "git_history_requirement"
	ToolQuestionForPlanner    ToolName = "question_for_planner"
	ToolPlanExecuteComplete   ToolName = "plan_execute_completed"
)

func ParseToolName(tool string) (ToolName, bool) {
	value := ToolName(strings.TrimSpace(tool))
	for _, known := range ToolNames() {
		if value == known {
			return known, true
		}
	}

	return "", false
}

func ToolNames() []ToolName {
	return []ToolName{
		ToolSubmitFeaturePlan,
		ToolSourceSatisfied,
		ToolAskPlannerQuestion,
		ToolGitHistoryRequirement,
		ToolQuestionForPlanner,
		ToolPlanExecuteComplete,
	}
}
