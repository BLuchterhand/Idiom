package app

import "testing"

func TestDirectPromptBudgetsRemainBoundedInNonInteractiveMode(t *testing.T) {
	route := turnRoutePlan{Family: "conversation", Contract: "direct_answer"}

	if shouldUnboundDirectPromptBudgets(true, route) {
		t.Fatal("non-interactive prompt mode must honor configured budgets")
	}

	if !shouldUnboundDirectPromptBudgets(false, route) {
		t.Fatal("interactive TUI conversation mode should retain unbounded investigation budgets")
	}
}
