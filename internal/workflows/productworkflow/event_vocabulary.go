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

var behavioralCoverageEventsByState = map[string]ProductWorkflowEventType{
	BehavioralCoveragePromotionStateProposed:   ProductWorkflowEventBehavioralCoverageProposed,
	BehavioralCoveragePromotionStatePrepared:   ProductWorkflowEventBehavioralCoveragePrepared,
	BehavioralCoveragePromotionStateReviewed:   ProductWorkflowEventBehavioralCoverageReviewed,
	BehavioralCoveragePromotionStateAccepted:   ProductWorkflowEventBehavioralCoverageAccepted,
	BehavioralCoveragePromotionStateRejected:   ProductWorkflowEventBehavioralCoverageRejected,
	BehavioralCoveragePromotionStateSuperseded: ProductWorkflowEventBehavioralCoverageSuperseded,
}

func ProductWorkflowBehavioralCoverageEvent(state string) (ProductWorkflowEventType, bool) {
	event, ok := behavioralCoverageEventsByState[strings.TrimSpace(state)]
	return event, ok
}

func KnownProductWorkflowBehavioralCoverageEvent(event ProductWorkflowEventType) bool {
	for _, known := range behavioralCoverageEventsByState {
		if known == event {
			return true
		}
	}
	return false
}
