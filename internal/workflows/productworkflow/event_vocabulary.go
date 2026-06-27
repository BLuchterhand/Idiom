package productworkflow

import "strings"

type ProductWorkflowEventType string
type ProductWorkflowEventSource string

const (
	ProductWorkflowEventCoverageGateEvaluated        ProductWorkflowEventType = "coverage_gate_evaluated"
	ProductWorkflowEventTransitionApplied            ProductWorkflowEventType = "product_workflow_transition_applied"
	ProductWorkflowEventBehavioralCoverageProposed   ProductWorkflowEventType = "behavioral_coverage_proposed"
	ProductWorkflowEventBehavioralCoveragePrepared   ProductWorkflowEventType = "behavioral_coverage_prepared"
	ProductWorkflowEventBehavioralCoverageReviewed   ProductWorkflowEventType = "behavioral_coverage_reviewed"
	ProductWorkflowEventBehavioralCoverageAccepted   ProductWorkflowEventType = "behavioral_coverage_accepted"
	ProductWorkflowEventBehavioralCoverageRejected   ProductWorkflowEventType = "behavioral_coverage_rejected"
	ProductWorkflowEventBehavioralCoverageSuperseded ProductWorkflowEventType = "behavioral_coverage_superseded"
	ProductWorkflowEventStatusMigrationPlanned       ProductWorkflowEventType = "status_migration_planned"

	ProductWorkflowEventSourceTransitionApply        ProductWorkflowEventSource = "product_workflow_transition_apply"
	ProductWorkflowEventSourceTDDBriefPromotion      ProductWorkflowEventSource = "product_tdd_promote_coverage"
	ProductWorkflowEventSourceStatusMigrationPlanner ProductWorkflowEventSource = "product_status_migration_planner"
	ProductWorkflowEventSourceWorkflowTransition     ProductWorkflowEventSource = "workflow_transition"
	ProductWorkflowEventSourceCoverageGate           ProductWorkflowEventSource = "coverage_gate"
	ProductWorkflowEventSourceBehavioralCoverage     ProductWorkflowEventSource = "behavioral_coverage"
	ProductWorkflowEventSourceStatusMigration        ProductWorkflowEventSource = "status_migration"
	ProductWorkflowEventSourceProductWorkflowStatus  ProductWorkflowEventSource = "product_workflow_status"
)

func ProductWorkflowBehavioralCoverageEvent(state string) (ProductWorkflowEventType, bool) {
	switch strings.TrimSpace(state) {
	case BehavioralCoveragePromotionStateProposed:
		return ProductWorkflowEventBehavioralCoverageProposed, true
	case BehavioralCoveragePromotionStatePrepared:
		return ProductWorkflowEventBehavioralCoveragePrepared, true
	case BehavioralCoveragePromotionStateReviewed:
		return ProductWorkflowEventBehavioralCoverageReviewed, true
	case BehavioralCoveragePromotionStateAccepted:
		return ProductWorkflowEventBehavioralCoverageAccepted, true
	case BehavioralCoveragePromotionStateRejected:
		return ProductWorkflowEventBehavioralCoverageRejected, true
	case BehavioralCoveragePromotionStateSuperseded:
		return ProductWorkflowEventBehavioralCoverageSuperseded, true
	default:
		return "", false
	}
}

func KnownProductWorkflowBehavioralCoverageEvent(event ProductWorkflowEventType) bool {
	switch event {
	case ProductWorkflowEventBehavioralCoverageProposed,
		ProductWorkflowEventBehavioralCoveragePrepared,
		ProductWorkflowEventBehavioralCoverageReviewed,
		ProductWorkflowEventBehavioralCoverageAccepted,
		ProductWorkflowEventBehavioralCoverageRejected,
		ProductWorkflowEventBehavioralCoverageSuperseded:
		return true
	default:
		return false
	}
}
