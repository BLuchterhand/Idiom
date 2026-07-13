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

// PlanDocumentEditScopeInput describes the edit authority that exists before
// and after already-resolved plan documents are applied. It is used when a
// caller must recompute scope after refinement or recovery without resolving
// document references again.
type PlanDocumentEditScopeInput struct {
	WorkspaceRoot string
	RequiredPaths []string
	AllowedPaths  []string
	PlanDocuments []PlanDocument
}

// PlanDocumentEditScope keeps required coverage authority separate from
// optional candidate scope. A path present in both inputs or introduced by a
// plan document is owned by RequiredPaths and omitted from AllowedPaths.
type PlanDocumentEditScope struct {
	RequiredPaths []string
	AllowedPaths  []string
}
