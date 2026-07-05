package featureplanner

type ToolName string

const (
	ToolSubmitFeaturePlan     ToolName = "submit_feature_plan"
	ToolSourceSatisfied       ToolName = "source_satisfied"
	ToolAskPlannerQuestion    ToolName = "ask_planner_question"
	ToolGitHistoryRequirement ToolName = "git_history_requirement"
	ToolQuestionForPlanner    ToolName = "question_for_planner"
	ToolPlanExecuteComplete   ToolName = "plan_execute_completed"
)
