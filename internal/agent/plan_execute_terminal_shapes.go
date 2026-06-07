package agent

const (
	planExecuteTerminalSubmitPlan      = "submit_plan"
	planExecuteTerminalSourceSatisfied = "Decision: source-satisfied"
	planExecuteTerminalQuestion        = "<<QUESTION>>:"
)

func planExecuteTerminalSourceSatisfiedOrQuestion() []string {
	return []string{planExecuteTerminalSourceSatisfied, planExecuteTerminalQuestion}
}

func planExecuteTerminalSubmitPlanSourceSatisfiedQuestion() []string {
	return []string{planExecuteTerminalSubmitPlan, planExecuteTerminalSourceSatisfied, planExecuteTerminalQuestion}
}

func planExecuteTerminalSubmitPlanOnly() []string {
	return []string{planExecuteTerminalSubmitPlan}
}
