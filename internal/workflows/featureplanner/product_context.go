package featureplanner

import (
	"fmt"
	"strings"

	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/platform/textutil"
	"idiom/internal/workflows/productworkflow"
)

// ProductWorkflowBrief carries the product-workflow evidence the planner
// needs to produce a grounded feature plan. The CLI layer populates it
// from the product workflow effort artifacts and passes it through RunOptions.
type ProductWorkflowBrief struct {
	EffortID                  string                                           `json:"effort_id"`
	TaskID                    string                                           `json:"task_id"`
	EffortRoot                string                                           `json:"effort_root"`
	StatusBucket              string                                           `json:"status_bucket,omitempty"`
	CurrentPhase              string                                           `json:"current_phase,omitempty"`
	CurrentState              string                                           `json:"current_state,omitempty"`
	NextValidActions          []string                                         `json:"next_valid_actions,omitempty"`
	SourceSatisfiedDelivery   *productworkflow.SourceSatisfiedDeliveryCloseout `json:"source_satisfied_delivery,omitempty"`
	PRDRequirements           []PRDRequirementBrief                            `json:"prd_requirements"`
	AcceptedADRIDs            []string                                         `json:"accepted_adr_ids"`
	AcceptedADRPaths          []string                                         `json:"accepted_adr_paths"`
	TDDObligations            []TDDObligationBrief                             `json:"tdd_obligations"`
	CoverageTier              string                                           `json:"coverage_tier"`
	CoverageGatePassed        bool                                             `json:"coverage_gate_passed"`
	CoverageContract          *CoverageContractRef                             `json:"coverage_contract,omitempty"`
	AcceptedCoverage          *AcceptedBehavioralCoverage                      `json:"accepted_coverage,omitempty"`
	CoverageBlockers          []string                                         `json:"coverage_blockers"`
	CoverageBlockerKinds      []string                                         `json:"coverage_blocker_kinds,omitempty"`
	FilesToModify             []string                                         `json:"files_to_modify"`
	ContextEvidencePaths      []string                                         `json:"context_evidence_paths,omitempty"`
	SourceFacts               []SourceFactBrief                                `json:"source_facts,omitempty"`
	AuthorizedNewPackageRoots []string                                         `json:"authorized_new_package_roots,omitempty"`
	RequiredBehavior          []string                                         `json:"required_behavior"`
	Tests                     []string                                         `json:"tests"`
	Acceptance                []string                                         `json:"acceptance"`
	Constraints               []string                                         `json:"constraints"`
	Decisions                 []string                                         `json:"decisions"`
	Blockers                  []string                                         `json:"blockers"`
	ArtifactRootPath          string                                           `json:"artifact_root_path"`
}

// BindProductTaskIdentity binds a fresh planner state to product task
// authority and rejects any resumed execution authority that lacks or conflicts
// with its durable task identity.
func BindProductTaskIdentity(state *FeatureState, productTaskID string) error {
	if state == nil {
		return fmt.Errorf("product-bound feature planner state is nil")
	}
	productTaskID = strings.TrimSpace(productTaskID)
	if productTaskID == "" {
		return fmt.Errorf("product-bound feature planner state requires task_id")
	}
	stateTaskID := strings.TrimSpace(state.TaskID)
	if stateTaskID == "" {
		if len(state.Atoms) > 0 || len(state.VerificationClosures) > 0 || len(state.ActiveRuns) > 0 ||
			strings.TrimSpace(state.Plan.Artifact.ArtifactID) != "" {
			return fmt.Errorf("existing product-bound feature planner state is missing durable task_id")
		}
		state.TaskID = productTaskID

		return nil
	}
	if stateTaskID != productTaskID {
		return fmt.Errorf(
			"product-bound feature planner state task_id %q does not match product task_id %q",
			stateTaskID,
			productTaskID,
		)
	}
	state.TaskID = stateTaskID

	return nil
}

func ProductWriteEnvelopePaths(brief *ProductWorkflowBrief) []string {
	if brief == nil {
		return nil
	}

	paths := plancontract.UniqueScopePaths(brief.FilesToModify)
	for _, rawRoot := range brief.AuthorizedNewPackageRoots {
		root := plancontract.NormalizeScopePath(strings.TrimSuffix(strings.TrimSpace(rawRoot), "/"))
		if root == "" {
			continue
		}

		paths = append(paths, root+"/**")
	}

	return plancontract.UniqueScopePaths(paths)
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

type SourceFactBrief struct {
	Path         string `json:"path,omitempty"`
	Symbol       string `json:"symbol,omitempty"`
	Signature    string `json:"signature,omitempty"`
	Anchor       string `json:"anchor,omitempty"`
	AuthorityRef string `json:"authority_ref,omitempty"`
}

func (brief ProductWorkflowBrief) AllowsFeaturePlanDispatch() bool {
	if brief.IsClosedSourceSatisfied() {
		return false
	}

	if len(brief.Blockers) > 0 {
		return false
	}

	if brief.CoverageGatePassed && len(brief.CoverageBlockers) == 0 {
		return true
	}

	return brief.coveragePendingImplementationVerification()
}

func (brief ProductWorkflowBrief) IsClosedSourceSatisfied() bool {
	if !textutil.EqualTrimFold(brief.StatusBucket, "closed") ||
		!textutil.EqualTrimFold(brief.CurrentPhase, string(productworkflow.PhaseComplete)) ||
		!textutil.EqualTrimFold(brief.CurrentState, productworkflow.WorkflowStateClosed) ||
		brief.SourceSatisfiedDelivery == nil {
		return false
	}
	if strings.TrimSpace(brief.SourceSatisfiedDelivery.EffortID) != strings.TrimSpace(brief.EffortID) ||
		strings.TrimSpace(brief.SourceSatisfiedDelivery.TaskID) != strings.TrimSpace(brief.TaskID) {
		return false
	}

	return productworkflow.ValidateSourceSatisfiedDeliveryCloseout(*brief.SourceSatisfiedDelivery) == nil
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
