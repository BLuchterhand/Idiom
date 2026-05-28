package featureplanner

// ProductWorkflowBrief carries the product-workflow evidence the planner
// needs to produce a grounded feature plan. The CLI layer populates it
// from the product workflow effort artifacts and passes it through RunOptions.
type ProductWorkflowBrief struct {
	EffortID           string                `json:"effort_id"`
	TaskID             string                `json:"task_id"`
	EffortRoot         string                `json:"effort_root"`
	PRDRequirements    []PRDRequirementBrief `json:"prd_requirements"`
	AcceptedADRIDs     []string              `json:"accepted_adr_ids"`
	AcceptedADRPaths   []string              `json:"accepted_adr_paths"`
	TDDObligations     []TDDObligationBrief  `json:"tdd_obligations"`
	CoverageTier       string                `json:"coverage_tier"`
	CoverageGatePassed bool                  `json:"coverage_gate_passed"`
	CoverageBlockers   []string              `json:"coverage_blockers"`
	FilesToModify      []string              `json:"files_to_modify"`
	RequiredBehavior   []string              `json:"required_behavior"`
	Tests              []string              `json:"tests"`
	Acceptance         []string              `json:"acceptance"`
	Constraints        []string              `json:"constraints"`
	Decisions          []string              `json:"decisions"`
	Blockers           []string              `json:"blockers"`
	ArtifactRootPath   string                `json:"artifact_root_path"`
}

type PRDRequirementBrief struct {
	ID          string   `json:"id"`
	Priority    string   `json:"priority"`
	Statement   string   `json:"statement"`
	Testability string   `json:"testability"`
	Metric      string   `json:"metric"`
	Obligations []string `json:"tdd_obligations"`
}

type TDDObligationBrief struct {
	ID             string `json:"id"`
	Behavior       string `json:"behavior"`
	Command        string `json:"command"`
	RequiredBefore string `json:"required_before"`
	Status         string `json:"status"`
}

