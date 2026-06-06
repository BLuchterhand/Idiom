package featureplanner

import "strings"

// ProductWorkflowBrief carries the product-workflow evidence the planner
// needs to produce a grounded feature plan. The CLI layer populates it
// from the product workflow effort artifacts and passes it through RunOptions.
type ProductWorkflowBrief struct {
	EffortID                  string                `json:"effort_id"`
	TaskID                    string                `json:"task_id"`
	EffortRoot                string                `json:"effort_root"`
	PRDRequirements           []PRDRequirementBrief `json:"prd_requirements"`
	AcceptedADRIDs            []string              `json:"accepted_adr_ids"`
	AcceptedADRPaths          []string              `json:"accepted_adr_paths"`
	TDDObligations            []TDDObligationBrief  `json:"tdd_obligations"`
	CoverageTier              string                `json:"coverage_tier"`
	CoverageGatePassed        bool                  `json:"coverage_gate_passed"`
	CoverageBlockers          []string              `json:"coverage_blockers"`
	CoverageBlockerKinds      []string              `json:"coverage_blocker_kinds,omitempty"`
	FilesToModify             []string              `json:"files_to_modify"`
	ContextEvidencePaths      []string              `json:"context_evidence_paths,omitempty"`
	AuthorizedNewPackageRoots []string              `json:"authorized_new_package_roots,omitempty"`
	RequiredBehavior          []string              `json:"required_behavior"`
	Tests                     []string              `json:"tests"`
	Acceptance                []string              `json:"acceptance"`
	Constraints               []string              `json:"constraints"`
	Decisions                 []string              `json:"decisions"`
	Blockers                  []string              `json:"blockers"`
	ArtifactRootPath          string                `json:"artifact_root_path"`
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

func (brief ProductWorkflowBrief) AllowsFeaturePlanDispatch() bool {
	if len(brief.Blockers) > 0 {
		return false
	}
	if brief.CoverageGatePassed && len(brief.CoverageBlockers) == 0 {
		return true
	}
	return brief.coveragePendingImplementationVerification()
}

func (brief ProductWorkflowBrief) coveragePendingImplementationVerification() bool {
	if brief.CoverageGatePassed || len(brief.CoverageBlockers) == 0 || len(brief.TDDObligations) == 0 {
		return false
	}
	if len(brief.CoverageBlockerKinds) > 0 {
		for _, kind := range brief.CoverageBlockerKinds {
			if strings.TrimSpace(kind) != "coverage_tdd_evidence_missing" {
				return false
			}
		}
	} else {
		for _, blocker := range brief.CoverageBlockers {
			lower := strings.ToLower(strings.TrimSpace(blocker))
			if !strings.Contains(lower, "tdd") || !strings.Contains(lower, "verification evidence") {
				return false
			}
		}
	}
	for _, obligation := range brief.TDDObligations {
		if !obligation.isDispatchReadyPendingImplementationTDD() {
			return false
		}
	}
	return true
}

func (obligation TDDObligationBrief) isDispatchReadyPendingImplementationTDD() bool {
	if strings.TrimSpace(obligation.ID) == "" || !looksExecutableCommand(obligation.Command) {
		return false
	}
	if !requiredBeforeImplementation(obligation.RequiredBefore) {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(obligation.Status)) {
	case "", "pending", "planned", "open":
		return true
	default:
		return false
	}
}
