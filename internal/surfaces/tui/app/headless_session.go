package app

import (
	runtimesession "idiom/internal/runtime/session"
	"idiom/internal/runtime/workerruntime"
)

// ConfigureHeadlessSession applies the complete non-interactive runtime
// contract in one place. Single-prompt and turns-file modes intentionally use
// the same configuration path.
func (m *Model) ConfigureHeadlessSession(opts runtimesession.Options) {
	if m == nil {
		return
	}

	m.SetForcedTaskFamily(opts.TaskFamily)
	m.SetPlannerOwnedVerification(opts.PlannerOwnedVerification)
	m.SetMaxTokensOverride(opts.MaxTokensOverride)
	m.SetDisableThinkingOverride(opts.DisableThinking)
	m.SetForcedReadinessOpenObligations(opts.ReviewRepairOpenObligations)
	m.SetNonInteractive(true)
	m.planExecuteSourceSatisfiedObserver = opts.OnSourceSatisfied

	if opts.UseActiveModel {
		m.UseConfigPlannerModel()
	}
	if workerruntime.TaskFamilyIsPlanThenExecutePlanner(opts.TaskFamily) {
		m.SetPlanExecuteScope(opts.ScopeContract, opts.AuthorizedNewFiles)
	}
}
