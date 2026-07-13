package productworkflow

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

type ProductWorkflowEventType string
type ProductWorkflowEventSource string

const (
	productWorkflowDefaultEventsPath = "events.jsonl"

	ProductWorkflowEventCoverageGateEvaluated        ProductWorkflowEventType = "coverage_gate_evaluated"
	ProductWorkflowEventTransitionApplied            ProductWorkflowEventType = "product_workflow_transition_applied"
	ProductWorkflowEventBehavioralCoverageProposed   ProductWorkflowEventType = "behavioral_coverage_proposed"
	ProductWorkflowEventBehavioralCoveragePrepared   ProductWorkflowEventType = "behavioral_coverage_prepared"
	ProductWorkflowEventBehavioralCoverageReviewed   ProductWorkflowEventType = "behavioral_coverage_reviewed"
	ProductWorkflowEventBehavioralCoverageAccepted   ProductWorkflowEventType = "behavioral_coverage_accepted"
	ProductWorkflowEventBehavioralCoverageRejected   ProductWorkflowEventType = "behavioral_coverage_rejected"
	ProductWorkflowEventBehavioralCoverageSuperseded ProductWorkflowEventType = "behavioral_coverage_superseded"
	ProductWorkflowEventStatusMigrationPlanned       ProductWorkflowEventType = "status_migration_planned"
	ProductWorkflowEventSourceSatisfiedRecorded      ProductWorkflowEventType = "source_satisfied_delivery_recorded"

	ProductWorkflowEventSourceTransitionApply        ProductWorkflowEventSource = "product_workflow_transition_apply"
	ProductWorkflowEventSourceTDDBriefPromotion      ProductWorkflowEventSource = "product_tdd_promote_coverage"
	ProductWorkflowEventSourceStatusMigrationPlanner ProductWorkflowEventSource = "product_status_migration_planner"
	ProductWorkflowEventSourceWorkflowTransition     ProductWorkflowEventSource = "workflow_transition"
	ProductWorkflowEventSourceCoverageGate           ProductWorkflowEventSource = "coverage_gate"
	ProductWorkflowEventSourceBehavioralCoverage     ProductWorkflowEventSource = "behavioral_coverage"
	ProductWorkflowEventSourceStatusMigration        ProductWorkflowEventSource = "status_migration"
	ProductWorkflowEventSourceProductWorkflowStatus  ProductWorkflowEventSource = "product_workflow_status"
	ProductWorkflowEventSourceSatisfiedDelivery      ProductWorkflowEventSource = "source_satisfied_delivery"
)

// productWorkflowEventsPath resolves the single event-ledger authority carried
// by workflow_state. Missing or unsafe paths fail closed; callers must not
// substitute the historical default for a state that omitted the contract.
func productWorkflowEventsPath(state WorkflowState) (string, error) {
	rawPath := strings.TrimSpace(state.ArtifactPaths["events"])
	cleanPath, ok := normalizedSafeEffortPath(rawPath)
	if !ok {
		return "", fmt.Errorf("workflow_state artifact_paths.events must be a safe relative path")
	}
	if err := validateProductWorkflowEventsPathConflicts(cleanPath, state.ArtifactPaths); err != nil {
		return "", err
	}

	return filepath.FromSlash(cleanPath), nil
}

func validateProductWorkflowEventsPathConflicts(eventsPath string, artifactPaths map[string]string) error {
	fixedAuthorities := []struct {
		name string
		path string
	}{
		{name: "workflow state", path: WorkflowStateFileName},
		{name: "PRD", path: "prd.md"},
		{name: "TDD ledger", path: "tdd.json"},
		{name: "ADR authority", path: "adrs"},
		{name: "brief authority", path: "briefs"},
		{name: "product mutation lock", path: productWorkflowMutationLockRelPath},
		{name: "efforts index lock", path: productStatusMigrationIndexLockRelPath},
		{name: "status migration marker", path: productStatusMigrationPendingMarkerName},
		{name: "ADR transaction", path: adrBatchTransactionRelPath},
		{name: "transition transaction", path: productWorkflowTransitionTransactionRelPath},
		{name: "status migration transaction", path: productStatusMigrationTransactionRelPath},
	}
	for _, authority := range fixedAuthorities {
		if productWorkflowArtifactPathsOverlap(eventsPath, authority.path) {
			return fmt.Errorf("workflow_state artifact_paths.events %q overlaps %s path %q", eventsPath, authority.name, authority.path)
		}
	}

	names := make([]string, 0, len(artifactPaths))
	for name := range artifactPaths {
		if name != "events" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		rawPath := strings.TrimSpace(artifactPaths[name])
		if rawPath == "" {
			continue
		}
		otherPath, ok := normalizedSafeEffortPath(rawPath)
		if !ok {
			return fmt.Errorf("workflow_state artifact_paths.%s must be a safe relative path before events authority can be resolved", name)
		}
		if productWorkflowArtifactPathsOverlap(eventsPath, otherPath) {
			return fmt.Errorf("workflow_state artifact_paths.events %q overlaps artifact_paths.%s %q", eventsPath, name, otherPath)
		}
	}

	return nil
}

func productWorkflowArtifactPathsOverlap(left, right string) bool {
	left = strings.ToLower(filepath.ToSlash(filepath.Clean(filepath.FromSlash(left))))
	right = strings.ToLower(filepath.ToSlash(filepath.Clean(filepath.FromSlash(right))))

	return left == right || strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/")
}

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
