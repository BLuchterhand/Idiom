package implementationdispatch

import atomintakecontract "idiom/internal/contracts/atomintake"

type PlanDocument struct {
	Path                string
	Content             string
	EditPaths           []string
	AuthorizedNewFiles  []string
	TestObligations     []string
	ReferenceOnly       bool
	AllowDirtyAtomScope bool
}

type PlanDocumentPreflightResult struct {
	Path         string
	OK           bool
	AtomState    atomintakecontract.IntakeState
	Blockers     []string
	LintBlockers []string
	Warnings     []string
}

type PlanDocumentRefineResult struct {
	OriginalPath          string
	OutputPath            string
	OK                    bool
	ExitCode              int
	Stdout                string
	Stderr                string
	Err                   error
	Document              PlanDocument
	PostPreflightBlockers []PlanDocumentPreflightResult
}

type ResolutionBlocker struct {
	Ref        string
	Reason     string
	Candidates []string
}

type ScopeInput struct {
	Prompt          string
	WorkspaceRoot   string
	TargetRefs      []string
	RequiredTargets []string
	AllowedTargets  []string
	MutationCapable bool
	RouteReason     string
}

type ScopeResolution struct {
	RequiredPaths []string
	AllowedPaths  []string
	PlanDocuments []PlanDocument
	Blockers      []ResolutionBlocker
}
