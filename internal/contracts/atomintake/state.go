package atomintake

type IntakeState string

const (
	StateDispatchable     IntakeState = "dispatchable"
	StateComplete         IntakeState = "complete"
	StateSourceSatisfied  IntakeState = "source-satisfied"
	StateStaleNeedsRebase IntakeState = "stale-needs-rebase"
	StateFailedRefine     IntakeState = "failed-refine-draft"
	StateBlockedDirty     IntakeState = "blocked-dirty-files"
	StateMissingScope     IntakeState = "missing-scope"
	StateInvalidScope     IntakeState = "invalid-scope"
)

const (
	InvalidScopeOutsideProjectRoot     = "outside_project_root"
	InvalidScopeAmbiguousBareShorthand = "ambiguous_bare_shorthand"
)
