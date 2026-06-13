package agent

import "idiom/internal/contracts/plan"

const (
	planExecuteTerminalSubmitPlan      = "submit_plan"
	planExecuteTerminalSourceSatisfied = plancontract.SourceSatisfiedDecision
	planExecuteTerminalQuestion        = "<<QUESTION>>:"
)

func planExecuteTerminalSourceSatisfiedOrQuestion() []string {
	return []string{planExecuteTerminalSourceSatisfied, planExecuteTerminalQuestion}
}

func planExecuteTerminalSubmitPlanSourceSatisfiedQuestion() []string {
	return []string{planExecuteTerminalSubmitPlan, planExecuteTerminalSourceSatisfied, planExecuteTerminalQuestion}
}

func planExecuteTerminalSubmitPlanQuestion() []string {
	return []string{planExecuteTerminalSubmitPlan, planExecuteTerminalQuestion}
}

func planExecuteTerminalSubmitPlanOnly() []string {
	return []string{planExecuteTerminalSubmitPlan}
}
