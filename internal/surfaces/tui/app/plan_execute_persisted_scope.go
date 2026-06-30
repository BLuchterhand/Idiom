package app

import (
	"strings"

	"idiom/internal/workflows/planexecute"
	planexecuteadapters "idiom/internal/workflows/planexecute/adapters"
)

func (m *Model) persistedPlanExecuteScopeForContinuation(input string) (planexecute.ContinuationScope, bool) {
	if m == nil || strings.TrimSpace(m.cfg.Workspace.Root) == "" {
		return planexecute.ContinuationScope{}, false
	}

	allow, _, ok := planExecuteApprovalDecisionFromOperatorInput(input)
	if !ok || !allow {
		return planexecute.ContinuationScope{}, false
	}

	scope, ok, err := planexecute.LoadApprovedContinuationScope(m.cfg.Workspace.Root, planexecute.ContinuationScopeRequest{
		Prompt: input,
	}, planexecuteadapters.Dependencies())
	if err != nil || !ok {
		return planexecute.ContinuationScope{}, false
	}

	return scope, true
}
